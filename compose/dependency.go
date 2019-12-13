package compose

type Dependency struct {
	service string
	channel chan bool
}

func NewDependency(service string) Dependency {
	return Dependency{
		service: service,
		channel: make(chan bool),
	}
}

func (d Dependency) Wait() {
	<-d.channel
}