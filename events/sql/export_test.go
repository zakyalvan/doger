package eventsql

// TablesOf returns the tables of t, for the black-box tests.
func TablesOf(t *Transport) Tables { return t.cfg.tables }
