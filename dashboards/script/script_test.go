package main

import (
	"testing"
)

func TestSelectorRegexp(t *testing.T) {
	testCases := []struct {
		name    string
		in, out string
	}{
		{
			name: "pass-as-it-is",
			in:   "abcdefghi1233((*_)LL",
			out:  "abcdefghi1233((*_)LL",
		},
		{
			name: "pass-no-selector",
			in:   "libp2p_hello_world[$__interval]",
			out:  "libp2p_hello_world[$__interval]",
		},
		{
			name: "convert selector",
			in:   `libp2p_hello_world{job="kubo"}[$__interval]`,
			out:  `libp2p_hello_world{job="kubo",instance=~\"$instance\"}[$__interval]`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.out != transformSelector(tc.in) {
				t.Errorf("conversion failed for %s: expected: %s got: %s", tc.in, tc.out,
					transformSelector(tc.in))
			}
		})
	}
}

func TestNoSelectorRegexp(t *testing.T) {
	testCases := []struct {
		name    string
		in, out string
	}{
		{
			name: "pass-as-it-is",
			in:   "abcdefghi1233((*_)LL",
			out:  "abcdefghi1233((*_)LL",
		},
		{
			name: "convert-no-selector",
			in:   "libp2p_hello_world[$__interval]",
			out:  `libp2p_hello_world{instance=~\"$instance\"}[$__interval]`,
		},
		{
			name: "leave-selector-as-is",
			in:   `libp2p_hello_world{job="kubo"}[$__interval]`,
			out:  `libp2p_hello_world{job="kubo"}[$__interval]`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.out != transformNoSelector(tc.in) {
				t.Errorf("conversion failed for %s: expected: %s got: %s", tc.in, tc.out,
					transformNoSelector(tc.in))
			}
		})
	}
}
