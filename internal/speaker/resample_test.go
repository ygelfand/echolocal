package speaker

import (
	"math"
	"testing"

	"github.com/ygelfand/echolocal/internal/settings"
)

// sine is n samples of a sine at hz, sampled at rate.
func sine(hz float64, rate int, n int, amp float64) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return out
}

// energyAt measures how much of a signal sits at hz, by correlating against that frequency.
func energyAt(x []int16, hz float64, rate int) float64 {
	var re, im float64
	for i, v := range x {
		a := 2 * math.Pi * hz * float64(i) / float64(rate)
		re += float64(v) * math.Cos(a)
		im -= float64(v) * math.Sin(a)
	}
	return math.Hypot(re, im) / float64(len(x))
}

// left takes one channel of an interleaved stereo buffer.
func left(x []int16) []int16 {
	out := make([]int16, 0, len(x)/Channels)
	for i := 0; i < len(x); i += Channels {
		out = append(out, x[i])
	}
	return out
}

// A 3 kHz tone upsampled to 48 kHz has images at 13 kHz and 19 kHz. Holding the sample leaves them
// most of the way intact, which is what makes speech sound gritty; filtering has to push them well
// down while leaving the tone itself alone.
func TestUpsamplerRejectsImages(t *testing.T) {
	const (
		hz    = 3000
		image = 2*VoiceRate - hz // 29 kHz folds to 19 kHz at 48 kHz output
		other = VoiceRate - hz   // 13 kHz
	)
	in := sine(hz, VoiceRate, 4096, 8000)

	var u sinc
	got := left(u.Run(in, nil))
	was := left(hold{}.Run(in, nil))

	// Skip the filter's start-up, where the history is still filling.
	got, was = got[256:], was[256:]

	wanted := energyAt(got, hz, Rate)
	if ref := energyAt(was, hz, Rate); wanted < ref*0.7 {
		t.Errorf("the tone itself lost too much: %.1f against %.1f held", wanted, ref)
	}

	for _, f := range []float64{other, image} {
		ours, theirs := energyAt(got, f, Rate), energyAt(was, f, Rate)
		t.Logf("image at %5.0f Hz: %8.2f filtered, %8.2f held, tone %8.2f", f, ours, theirs, wanted)
		if ours > theirs/8 {
			t.Errorf("image at %.0f Hz is %.2f, only %.1fx below the held version's %.2f",
				f, ours, theirs/ours, theirs)
		}
		if ours > wanted/100 {
			t.Errorf("image at %.0f Hz is %.2f, within 40 dB of the tone at %.2f", f, ours, wanted)
		}
	}
}

// Silence in, silence out: any leaked offset would be a click at the start of every reply.
func TestUpsamplerKeepsSilenceSilent(t *testing.T) {
	var u sinc
	got := u.Run(make([]int16, 512), nil)
	for i, v := range got {
		if v != 0 {
			t.Fatalf("sample %d of silence is %d", i, v)
		}
	}
}

// A chunked utterance has to come out the same as the whole thing at once, or every chunk boundary
// is a discontinuity.
func TestUpsamplerIsContinuousAcrossChunks(t *testing.T) {
	in := sine(1000, VoiceRate, 900, 6000)

	var whole sinc
	want := whole.Run(in, nil)

	var chunked sinc
	var got []int16
	for off := 0; off < len(in); off += 137 {
		got = chunked.Run(in[off:min(off+137, len(in))], got)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d is %d chunked, %d whole", i, got[i], want[i])
		}
	}
}

// Every registered option has to produce the same amount of audio, or picking one would change the
// reply's length and pitch.
func TestResamplersAgreeOnRate(t *testing.T) {
	in := sine(1000, VoiceRate, 300, 6000)

	for _, r := range Resamplings() {
		u, settled := NewResampler(r)
		if settled != r {
			t.Errorf("%s came back as %s, so it is not registered", r, settled)
			continue
		}
		if got, want := len(u.Run(in, nil)), len(in)*VoiceUpsample*Channels; got != want {
			t.Errorf("%s produced %d samples, want %d", r, got, want)
		}
	}
}

// The ordering the setting is offered in is the ordering of how much of the image it removes. If
// this ever inverts, the labels are lying about what the choice does.
func TestResamplersRankByImageRejection(t *testing.T) {
	const hz = 3000
	in := sine(hz, VoiceRate, 4096, 8000)

	image := make(map[settings.Resampling]float64)
	for _, r := range []settings.Resampling{settings.ResampleSinc, settings.ResampleLinear, settings.ResampleHold} {
		u, _ := NewResampler(r)
		out := left(u.Run(in, nil))[256:]
		image[r] = energyAt(out, 2*VoiceRate-hz, Rate)
		t.Logf("%-14s image %8.2f, tone %8.2f", r.Label(), image[r], energyAt(out, hz, Rate))
	}

	if image[settings.ResampleSinc] >= image[settings.ResampleLinear] {
		t.Errorf("the filter leaves %.2f of the image, linear only %.2f",
			image[settings.ResampleSinc], image[settings.ResampleLinear])
	}
	if image[settings.ResampleLinear] >= image[settings.ResampleHold] {
		t.Errorf("linear leaves %.2f of the image, holding only %.2f",
			image[settings.ResampleLinear], image[settings.ResampleHold])
	}
}

func TestUpsamplerFillsBothChannels(t *testing.T) {
	var u sinc
	got := u.Run([]int16{1000, -1000, 500}, nil)
	if len(got) != 3*VoiceUpsample*Channels {
		t.Fatalf("got %d samples for 3 in, want %d", len(got), 3*VoiceUpsample*Channels)
	}
	for i := 0; i < len(got); i += Channels {
		if got[i] != got[i+1] {
			t.Fatalf("channels differ at %d: %d and %d", i, got[i], got[i+1])
		}
	}
}
