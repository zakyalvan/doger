// Command event-driven publishes typed events in-process and to other
// processes: an order service and two billing instances share an in-memory
// event transport, and each order is invoiced by one billing instance.
package main
