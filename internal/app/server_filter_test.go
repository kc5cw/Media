package app

import (
	"net/http/httptest"
	"testing"
)

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

func TestMediaFilterFromRequestDeviceFields(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/api/media?device_make=DJI&device_model=Mini%204%20Pro", nil)
	filter, err := mediaFilterFromRequest(req)
	if err != nil {
		t.Fatalf("mediaFilterFromRequest returned error: %v", err)
	}
	if filter.DeviceMake != "DJI" {
		t.Fatalf("DeviceMake = %q, want %q", filter.DeviceMake, "DJI")
	}
	if filter.DeviceModel != "Mini 4 Pro" {
		t.Fatalf("DeviceModel = %q, want %q", filter.DeviceModel, "Mini 4 Pro")
	}
	if filter.DeviceUnset {
		t.Fatalf("DeviceUnset = true, want false")
	}
}

func TestMediaFilterFromRequestDeviceUnknown(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/api/media?device_unknown=yes&device_make=DJI&device_model=Mavic", nil)
	filter, err := mediaFilterFromRequest(req)
	if err != nil {
		t.Fatalf("mediaFilterFromRequest returned error: %v", err)
	}
	if !filter.DeviceUnset {
		t.Fatalf("DeviceUnset = false, want true")
	}
	if filter.DeviceMake != "" || filter.DeviceModel != "" {
		t.Fatalf("DeviceMake/DeviceModel should be empty when device_unknown=yes, got %q/%q", filter.DeviceMake, filter.DeviceModel)
	}
}

func TestMediaFilterFromRequestRejectsInvalidDeviceUnknown(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/api/media?device_unknown=maybe", nil)
	_, err := mediaFilterFromRequest(req)
	if err == nil {
		t.Fatal("mediaFilterFromRequest expected error for invalid device_unknown")
	}
}
