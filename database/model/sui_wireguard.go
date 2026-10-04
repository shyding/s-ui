package model

import "encoding/json"

// SUIWireGuardNode stores a subscription-only WireGuard identity.  It is kept
// separate from Endpoint because loading the same identity into the server's
// sing-box core would make the client and server compete for one WireGuard
// peer endpoint.
type SUIWireGuardNode struct {
	ID      uint            `json:"id" gorm:"primaryKey;autoIncrement"`
	Tag     string          `json:"tag" gorm:"uniqueIndex;not null"`
	Port    int             `json:"port" gorm:"uniqueIndex;not null"`
	Options json.RawMessage `json:"-" gorm:"not null"`
}
