package services

// Database interface for dependency injection.
type Database interface {
	Query(sql string) string
}

// Cache interface for dependency injection.
type Cache interface {
	Get(key string) any
	Set(key string, value any)
}

// Logger interface for dependency injection.
type Logger interface {
	Log(message string)
}
