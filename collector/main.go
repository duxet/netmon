package collector

import "time"

// Options configures a collector.
type Options struct {
	// OnFlush, if set, is called after each round of collected flows has
	// been written to the database (about once a minute), with the time of
	// the write. It is called for rounds without any traffic too, so it can
	// drive work that depends on time passing. It is not called when writing
	// failed; that round's traffic is written with the next one. OnFlush runs
	// on the collector's goroutine and should return quickly.
	OnFlush func(time.Time)

	// Interface is the network interface to capture packets on, for the
	// collectors that capture them (macOS). Empty is the interface the
	// default route goes through.
	Interface string
}

// flushed reports a completed flush to OnFlush.
func (o Options) flushed(at time.Time) {
	if o.OnFlush != nil {
		o.OnFlush(at)
	}
}

type Collector struct {
	shutdownChan chan bool

	// done is optional. When set, it is closed once the collector has
	// finished shutting down, e.g. after writing out data it still held in
	// memory, and Shutdown waits for it.
	done chan struct{}
}

func (c *Collector) Shutdown() {
	c.shutdownChan <- true

	if c.done != nil {
		<-c.done
	}
}
