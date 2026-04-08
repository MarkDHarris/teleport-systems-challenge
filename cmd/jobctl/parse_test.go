package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseStartArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr bool
	}{
		{name: "empty", args: nil, want: nil, wantErr: true},
		{name: "strip double dash", args: []string{"--", "/bin/echo", "hi"}, want: []string{"/bin/echo", "hi"}, wantErr: false},
		{name: "no strip", args: []string{"/bin/echo", "hi"}, want: []string{"/bin/echo", "hi"}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStartArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				var ee *exitError
				if !errors.As(err, &ee) {
					t.Fatalf("want *exitError, got %T", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStartArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
