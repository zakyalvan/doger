package main

import "fmt"

// Cache represents a cache service.
type Cache struct {
	Data map[string]any
}

func (c *Cache) Get(key string) any {
	return c.Data[key]
}

func (c *Cache) Set(key string, value any) {
	c.Data[key] = value
}

func (c *Cache) HealthCheck() error {
	return nil
}

func (c *Cache) Shutdown() error {
	fmt.Println("Shutting down cache")
	return nil
}
