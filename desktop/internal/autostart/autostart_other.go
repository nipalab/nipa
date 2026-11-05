//go:build !linux && !darwin && !windows

package autostart

func enable(string) error { return ErrUnsupported }

func disable() error { return nil }
