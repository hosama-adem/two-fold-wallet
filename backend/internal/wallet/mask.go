package wallet

import (
	"fmt"

	"github.com/hosama-adem/twofold/backend/internal/phone"
)

// MaskPhone renders +251911223344 as "+251 9•• ••• 344".
// The caller confirms the person. They never see the full number.
func MaskPhone(e164 string) (string, error) {
	canonical, err := phone.Normalize(e164)
	if err != nil {
		return "", err
	}
	national := canonical[4:]
	if len(national) != 9 {
		return "", phone.ErrInvalid
	}
	return fmt.Sprintf("+251 %s•• ••• %s", national[:1], national[6:]), nil
}
