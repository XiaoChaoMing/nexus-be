package entity_test

import (
	"testing"
	"time"

	"github.com/minhnn/nexuslive-be/services/identity/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestUser_IsLocked(t *testing.T) {
	t.Parallel()

	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name string
		user entity.User
		want bool
	}{
		{name: "active", user: entity.User{Status: entity.UserActive}, want: false},
		{name: "locked forever", user: entity.User{Status: entity.UserLocked}, want: true},
		{name: "locked until future", user: entity.User{Status: entity.UserLocked, LockedUntil: &future}, want: true},
		{name: "lock expired", user: entity.User{Status: entity.UserLocked, LockedUntil: &past}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.user.IsLocked(now))
		})
	}
}
