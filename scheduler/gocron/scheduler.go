package dogergocron

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"github.com/kartaladev/doger"
)

var _ doger.HealthScheduler = (*Scheduler)(nil)

// DefaultInterval is the time between two rounds of the default job.
const DefaultInterval = 30 * time.Second

// ErrInvalidOption is matched by the error New returns for an invalid option.
var ErrInvalidOption = errors.New("dogergocron: invalid option")

// Option configures a Scheduler. It returns an error for an invalid value.
type Option func(*config) error

// config is the configuration New builds.
type config struct {
	interval      time.Duration
	definition    gocron.JobDefinition // wins over interval when set
	schedulerOpts []gocron.SchedulerOption
	jobOpts       []gocron.JobOption
	startAt       gocron.StartAtOption // when the first round runs
	shared        gocron.Scheduler     // used, not owned, when set
}

// Scheduler is a doger.HealthScheduler running each container's health
// rounds as a gocron job.
//
// Defaults: a duration job every DefaultInterval, the first round at once,
// the container's clock, and singleton mode with gocron.LimitModeReschedule,
// so a tick that finds a round still running is skipped instead of piling
// up. Each option overrides one default; gocron options given with
// WithSchedulerOptions and WithJobOptions are applied after the defaults, so
// they win over the defaults they override, except start-at options, which
// gocron combines: use WithStartAt to change when the first round runs.
//
// Health is per instance: do not use gocron's distributed elector or locker
// for this job, which would check the services of a single instance only.
type Scheduler struct {
	cfg config
}

// New returns a Scheduler configured by opts, or an error matching
// ErrInvalidOption.
func New(opts ...Option) (*Scheduler, error) {
	cfg := config{interval: DefaultInterval, startAt: gocron.WithStartImmediately()}
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	return &Scheduler{cfg: cfg}, nil
}

// WithInterval sets the interval of the default duration job. It must be
// positive.
func WithInterval(d time.Duration) Option {
	return func(cfg *config) error {
		if d <= 0 {
			return fmt.Errorf("%w: interval must be positive, got %v", ErrInvalidOption, d)
		}
		cfg.interval = d
		return nil
	}
}

// WithJobDefinition replaces the default duration job, for example with
// gocron.CronJob. It wins over WithInterval, whatever their order.
func WithJobDefinition(def gocron.JobDefinition) Option {
	return func(cfg *config) error {
		if def == nil {
			return fmt.Errorf("%w: job definition is nil", ErrInvalidOption)
		}
		cfg.definition = def
		return nil
	}
}

// WithStartAt sets when the first round runs, replacing the default
// gocron.WithStartImmediately(), for example
// gocron.WithStartDateTime(start). Pass it here rather than through
// WithJobOptions: gocron combines start-at options, so one given there runs
// in addition to the default immediate start.
func WithStartAt(opt gocron.StartAtOption) Option {
	return func(cfg *config) error {
		if opt == nil {
			return fmt.Errorf("%w: start-at option is nil", ErrInvalidOption)
		}
		cfg.startAt = opt
		return nil
	}
}

// WithSchedulerOptions adds options to the gocron scheduler each schedule
// creates, after the adapter's own (gocron.WithClock). Repeated options
// accumulate in order. They are not used with WithScheduler.
func WithSchedulerOptions(opts ...gocron.SchedulerOption) Option {
	return func(cfg *config) error {
		cfg.schedulerOpts = append(cfg.schedulerOpts, opts...)
		return nil
	}
}

// WithJobOptions adds options to the health job, after the adapter's own
// (its name, singleton mode and its start-at, see WithStartAt). Repeated
// options accumulate in order.
func WithJobOptions(opts ...gocron.JobOption) Option {
	return func(cfg *config) error {
		cfg.jobOpts = append(cfg.jobOpts, opts...)
		return nil
	}
}

// WithScheduler adds the health job to s, a gocron scheduler the
// application owns, instead of creating one per schedule: the adapter never
// starts or shuts s down, and stopping a schedule removes only its job. The
// container's clock is then ignored in favor of s's own clock.
func WithScheduler(s gocron.Scheduler) Option {
	return func(cfg *config) error {
		if s == nil {
			return fmt.Errorf("%w: scheduler is nil", ErrInvalidOption)
		}
		cfg.shared = s
		return nil
	}
}

// ScheduleHealthRounds implements doger.HealthScheduler: it schedules round
// as a gocron job on clock, and returns the function stopping it. Without
// WithScheduler, it creates and starts its own gocron scheduler, which stop
// shuts down within its context; a job gocron rejects is returned as an
// error, and leaves no goroutine behind.
func (s *Scheduler) ScheduleHealthRounds(clock clockwork.Clock, round func(ctx context.Context)) (func(ctx context.Context) error, error) {
	definition := s.cfg.definition
	if definition == nil {
		definition = gocron.DurationJob(s.cfg.interval)
	}
	task := gocron.NewTask(func(ctx context.Context) { round(ctx) })
	jobOpts := jobOptions(s.cfg)

	if s.cfg.shared != nil {
		rounds := &roundGauge{}
		task = gocron.NewTask(func(ctx context.Context) {
			if rounds.enter() {
				defer rounds.leave()
				round(ctx)
			}
		})
		job, err := s.cfg.shared.NewJob(definition, task, jobOpts...)
		if err != nil {
			return nil, err
		}
		return sharedStop(s.cfg.shared, job.ID(), rounds), nil
	}

	scheduler, err := gocron.NewScheduler(schedulerOptions(s.cfg, clock)...)
	if err != nil {
		return nil, err
	}
	if _, err := scheduler.NewJob(definition, task, jobOpts...); err != nil {
		_ = scheduler.Shutdown()
		return nil, err
	}
	scheduler.Start()

	return scheduler.ShutdownWithContext, nil
}

// sharedStop returns the stop of the job id on scheduler, a scheduler the
// application owns: it removes the job, which the owner may have removed
// already by shutting scheduler down, then waits within its context until no
// round of the job, counted by rounds, runs.
func sharedStop(scheduler gocron.Scheduler, id uuid.UUID, rounds *roundGauge) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := scheduler.RemoveJob(id); err != nil && !errors.Is(err, gocron.ErrJobNotFound) {
			return err
		}
		return rounds.stop(ctx)
	}
}

// roundGauge counts the rounds of a job in progress.
type roundGauge struct {
	mu      sync.Mutex
	running int
	idle    chan struct{} // closed once no round runs, while one ran
	stopped bool          // no round starts any more
}

// enter records a round starting, or reports false once stopped.
func (g *roundGauge) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.stopped {
		return false
	}
	if g.running == 0 {
		g.idle = make(chan struct{})
	}
	g.running++
	return true
}

// leave records a round returning.
func (g *roundGauge) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.running--
	if g.running == 0 {
		close(g.idle)
	}
}

// stop prevents rounds from starting, then waits until none runs, or returns
// ctx's error once ctx ends first.
func (g *roundGauge) stop(ctx context.Context) error {
	g.mu.Lock()
	g.stopped = true
	running, idle := g.running, g.idle
	g.mu.Unlock()
	if running == 0 {
		return nil
	}

	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// jobOptions returns the options of the health job: the adapter's name,
// singleton mode and start-at, then the consumer's.
func jobOptions(cfg config) []gocron.JobOption {
	return append([]gocron.JobOption{
		gocron.WithName("doger-health-round"),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
		gocron.WithStartAt(cfg.startAt),
	}, cfg.jobOpts...)
}

// schedulerOptions returns the options of an own gocron scheduler on clock,
// then the consumer's.
func schedulerOptions(cfg config, clock clockwork.Clock) []gocron.SchedulerOption {
	return append([]gocron.SchedulerOption{gocron.WithClock(clock)}, cfg.schedulerOpts...)
}
