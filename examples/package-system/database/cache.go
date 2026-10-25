package database

// Cache represents a caching service.
type Cache struct {
	Data map[string]any
}

func (c *Cache) Get(key string) any {
	return c.Data[key]
}

func (c *Cache) Set(key string, value any) {
	c.Data[key] = value
}
