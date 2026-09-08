package gitstate

type State interface {
	isState()
}

type Clean struct{}

func (Clean) isState() {}

type Dirty struct{}

func (Dirty) isState() {}
