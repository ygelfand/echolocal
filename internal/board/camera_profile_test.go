package board

import "testing"

func TestEveryCameraBoardNamesItsEncoderProfile(t *testing.T) {
	for _, b := range All {
		if b.Has(Camera) && b.CameraProfile == 0 {
			t.Errorf("%s has a camera and no encoder profile", b.Codename)
		}
	}
}
