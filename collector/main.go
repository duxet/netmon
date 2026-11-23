package collector

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
