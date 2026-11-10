package settings

import "testing"

func TestFilesZoomNormalize(t *testing.T) {
	for _, tc := range []struct {
		in, want int
		scale    float32
	}{{0, 0, 1}, {100, 0, 1}, {125, 125, 1.25}, {80, 80, 0.8}, {200, 200, 2}, {130, 0, 1}, {-10, 0, 1}} {
		s := Default()
		s.FilesZoom = tc.in
		s.Normalize()
		if s.FilesZoom != tc.want || s.FilesScale() != tc.scale {
			t.Errorf("FilesZoom %d: got %d (scale %v), want %d (scale %v)", tc.in, s.FilesZoom, s.FilesScale(), tc.want, tc.scale)
		}
	}
}
