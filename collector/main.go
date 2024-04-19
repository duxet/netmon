package collector

type Collector struct {
	shutdownChan chan bool
}

func (c *Collector) Shutdown() {
	c.shutdownChan <- true
}
