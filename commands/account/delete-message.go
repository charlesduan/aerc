package account

import (
	"errors"
	"time"

	"github.com/gdamore/tcell"

	"git.sr.ht/~sircmpwn/aerc2/widgets"
	"git.sr.ht/~sircmpwn/aerc2/worker/types"
)

func init() {
	register("delete-message", DeleteMessage)
}

func DeleteMessage(aerc *widgets.Aerc, args []string) error {
	if len(args) != 1 {
		return errors.New("Usage: :delete-message")
	}
	acct := aerc.SelectedAccount()
	if acct == nil {
		return errors.New("No account selected")
	}
	store := acct.Messages().Store()
	msg := acct.Messages().Selected()
	store.Delete([]uint32{msg.Uid}, func(msg types.WorkerMessage) {
		switch msg := msg.(type) {
		case *types.Done:
			aerc.PushStatus("Messages deleted.", 10*time.Second)
		case *types.Error:
			aerc.PushStatus(" "+msg.Error.Error(), 10*time.Second).
				Color(tcell.ColorDefault, tcell.ColorRed)
		}
	})
	return nil
}
