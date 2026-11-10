package access

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	const vmax = 3500 * time.Millisecond
	cases := []struct {
		name     string
		in       ClassifyInput
		wantType string
		wantConf string
	}{
		{"call, no duration", ClassifyInput{HadCall: true, DurationKnown: false, VisitorMax: vmax}, TypeVisitor, ConfMedium},
		{"call, short pulse", ClassifyInput{HadCall: true, Duration: 2 * time.Second, DurationKnown: true, VisitorMax: vmax}, TypeVisitor, ConfHigh},
		{"call, long pulse", ClassifyInput{HadCall: true, Duration: 5 * time.Second, DurationKnown: true, VisitorMax: vmax}, TypeVisitor, ConfLow},
		{"no call, no duration", ClassifyInput{HadCall: false, DurationKnown: false, VisitorMax: vmax}, TypeResident, ConfMedium},
		{"no call, long pulse", ClassifyInput{HadCall: false, Duration: 4500 * time.Millisecond, DurationKnown: true, VisitorMax: vmax}, TypeResident, ConfHigh},
		{"no call, short pulse (conflict)", ClassifyInput{HadCall: false, Duration: 2 * time.Second, DurationKnown: true, VisitorMax: vmax}, TypeUnknown, ConfLow},
		{"edge: duration == threshold with call", ClassifyInput{HadCall: true, Duration: vmax, DurationKnown: true, VisitorMax: vmax}, TypeVisitor, ConfHigh},
		{"edge: duration == threshold without call", ClassifyInput{HadCall: false, Duration: vmax, DurationKnown: true, VisitorMax: vmax}, TypeUnknown, ConfLow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.in)
			if got.Type != c.wantType || got.Confidence != c.wantConf {
				t.Errorf("Classify(%+v) = {%s,%s}; want {%s,%s}", c.in, got.Type, got.Confidence, c.wantType, c.wantConf)
			}
		})
	}
}
