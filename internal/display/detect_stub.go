//go:build !linux
// +build !linux

package display

type State struct {
	HasHDMI      bool
	HasDSI       bool
	HasAnyDRM    bool
	Framebuffers []string
	HasTouch     bool
}

func Detect() State {
	return State{}
}
