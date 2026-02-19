package app

import "testing"

func TestNormalizeKindFilterValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: "", wantErr: false},
		{in: "image", want: "image", wantErr: false},
		{in: "Images", want: "image", wantErr: false},
		{in: "jpg", want: "image", wantErr: false},
		{in: "video", want: "video", wantErr: false},
		{in: "VIDEOS", want: "video", wantErr: false},
		{in: "mp4", want: "video", wantErr: false},
		{in: "unknown", want: "", wantErr: true},
	}

	for _, tc := range cases {
		got, err := normalizeKindFilterValue(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("normalizeKindFilterValue(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeKindFilterValue(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeKindFilterValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
