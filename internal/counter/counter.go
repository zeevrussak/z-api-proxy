package counter

import "sync/atomic"

type Counter struct {
	handled  atomic.Int64
	rejected atomic.Int64
}

func New() *Counter {
	return &Counter{}
}

func (c *Counter) IncHandled()  { c.handled.Add(1) }
func (c *Counter) IncRejected() { c.rejected.Add(1) }

func (c *Counter) Handled() int64  { return c.handled.Load() }
func (c *Counter) Rejected() int64 { return c.rejected.Load() }
