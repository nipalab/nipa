package workspace

// Window is the main window's size and state when the app last closed it.
// Width and Height are the restored (not maximised) size.
type Window struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

// Size returns the stored size, falling back to the defaults for missing
// values and never below the minimum.
func (w Window) Size(defW, defH, minW, minH int) (int, int) {
	width, height := w.Width, w.Height
	if width <= 0 {
		width = defW
	}
	if height <= 0 {
		height = defH
	}
	return max(width, minW), max(height, minH)
}

// Closing is the state to store for a window closing at width x height:
// a maximised window keeps the previous restored size.
func (w Window) Closing(width, height int, maximised bool) Window {
	if maximised {
		return Window{Width: w.Width, Height: w.Height, Maximised: true}
	}
	return Window{Width: width, Height: height}
}
