package pluginmanager

import (
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/plugin"
)

func TestActivationAfterInstall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		forceEnable   bool
		wasRegistered bool
		wasEnabled    bool
		want          bool
	}{
		{name: "new install", forceEnable: true, want: true},
		{
			name:          "update running plugin",
			wasRegistered: true,
			wasEnabled:    true,
			want:          true,
		},
		{
			name:          "update stopped plugin",
			wasRegistered: true,
			wasEnabled:    false,
			want:          false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := activationAfterInstall(
				test.forceEnable,
				plugin.Status{Enabled: test.wasEnabled},
				test.wasRegistered,
			)
			if got != test.want {
				t.Fatalf("activation = %v, want %v", got, test.want)
			}
		})
	}
}
