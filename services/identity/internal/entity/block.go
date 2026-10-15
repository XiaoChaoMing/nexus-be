package entity

import "time"

// BlockMax is how many accounts one user may block (NXL-F BLOCK_MAX).
const BlockMax = 1000

// Block is one account the user blocked, with the blocked account's names.
type Block struct {
	UserID      string
	Username    string
	DisplayName string
	CreatedAt   time.Time
}

// BlockPair is one row of user_blocks (internal snapshot).
type BlockPair struct {
	BlockerID string
	BlockedID string
}
