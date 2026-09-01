package gotd

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestPrivateDialogFolder(t *testing.T) {
	t.Parallel()

	archived := &tg.Dialog{}
	archived.SetFolderID(1)
	tests := []struct {
		name     string
		result   *tg.MessagesPeerDialogs
		exists   bool
		folderID int
		wantErr  bool
	}{
		{
			name:    "missing dialog",
			result:  &tg.MessagesPeerDialogs{},
			exists:  false,
			wantErr: false,
		},
		{
			name: "main folder",
			result: &tg.MessagesPeerDialogs{
				Dialogs: []tg.DialogClass{&tg.Dialog{}},
			},
			exists:   true,
			folderID: 0,
		},
		{
			name: "archive folder",
			result: &tg.MessagesPeerDialogs{
				Dialogs: []tg.DialogClass{archived},
			},
			exists:   true,
			folderID: 1,
		},
		{
			name:    "nil result",
			result:  nil,
			wantErr: true,
		},
		{
			name: "unexpected dialog type",
			result: &tg.MessagesPeerDialogs{
				Dialogs: []tg.DialogClass{&tg.DialogFolder{}},
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			exists, folderID, err := privateDialogFolder(test.result)
			if (err != nil) != test.wantErr {
				t.Fatalf("privateDialogFolder() error = %v, wantErr=%t", err, test.wantErr)
			}
			if exists != test.exists || folderID != test.folderID {
				t.Fatalf(
					"privateDialogFolder() = (%t, %d), want (%t, %d)",
					exists,
					folderID,
					test.exists,
					test.folderID,
				)
			}
		})
	}
}
