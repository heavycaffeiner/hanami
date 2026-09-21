package bootstrap

import "sync/atomic"

// Admission records whether the application may accept normal work.
type Admission struct {
	open atomic.Bool
}

func NewAdmission() *Admission {
	return &Admission{}
}

func (a *Admission) Open() {
	a.open.Store(true)
}

func (a *Admission) Close() {
	a.open.Store(false)
}

func (a *Admission) IsOpen() bool {
	return a.open.Load()
}
