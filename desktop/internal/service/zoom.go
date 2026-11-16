package service

import (
	"fmt"
	"math"
	"strconv"
)

const (
	ZoomSettingKey = "nipa.zoom"
	MinZoom        = 0.5
	MaxZoom        = 2.0
)

func validZoom(level float64) bool {
	return !math.IsNaN(level) && level >= MinZoom && level <= MaxZoom
}

// SetZoom scales the whole window's content; 1 is the default size.
func (s *Service) SetZoom(level float64) error {
	if !validZoom(level) {
		return fmt.Errorf("zoom must be between %v and %v", MinZoom, MaxZoom)
	}
	if s.ZoomFunc != nil {
		s.ZoomFunc(level)
	}
	return nil
}

// StoredZoom is the zoom kept in the interface state, 1 when unset or invalid.
func StoredZoom(ui UIStore) float64 {
	state, err := ui.UI()
	if err != nil {
		return 1
	}
	level, err := strconv.ParseFloat(state[ZoomSettingKey], 64)
	if err != nil || !validZoom(level) {
		return 1
	}
	return level
}
