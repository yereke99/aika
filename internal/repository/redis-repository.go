package repository

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type ChatRepository struct {
	client *redis.Client
}

func NewRedisClient(client *redis.Client) *ChatRepository {
	return &ChatRepository{
		client: client,
	}
}

func (r *ChatRepository) AddUser(ctx context.Context, userID int64) error {
	key := "chat:users"
	isMember, err := r.client.SIsMember(ctx, key, userID).Result()
	if err != nil {
		return fmt.Errorf("failed to check user membership: %w", err)
	}

	if !isMember {
		if err := r.client.SAdd(ctx, key, userID).Err(); err != nil {
			return fmt.Errorf("failed to add user to set: %w", err)
		}
	}

	return nil
}

func (r *ChatRepository) FindPartner(ctx context.Context, userID int64) (int64, error) {
	key := "chat:users"
	users, err := r.client.SMembers(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get users from set: %w", err)
	}
	for _, user := range users {
		partnerID := user
		if partnerID != fmt.Sprintf("%d", userID) {
			if err := r.client.SRem(ctx, key, partnerID).Err(); err != nil {
				return 0, fmt.Errorf("failed to remove partner from set: %w", err)
			}
			return parseInt64(partnerID), nil
		}
	}
	return 0, nil
}

func (r *ChatRepository) SetPartner(ctx context.Context, userID, partnerID int64) error {
	key := fmt.Sprintf("chat:partner:%d", userID)
	if err := r.client.Set(ctx, key, partnerID, 0).Err(); err != nil {
		return fmt.Errorf("failed to set partner: %w", err)
	}
	return nil
}

func (r *ChatRepository) GetUserPartner(ctx context.Context, userID int64) (int64, error) {
	key := fmt.Sprintf("chat:partner:%d", userID)
	partnerID, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil // No partner
	} else if err != nil {
		return 0, fmt.Errorf("failed to get partner: %w", err)
	}
	return parseInt64(partnerID), nil
}

func (r *ChatRepository) RemoveUser(ctx context.Context, userID int64) error {
	// Remove user from set
	keyUsers := "chat:users"
	if err := r.client.SRem(ctx, keyUsers, userID).Err(); err != nil {
		return fmt.Errorf("failed to remove user from set: %w", err)
	}

	// Remove partner mapping
	keyPartner := fmt.Sprintf("chat:partner:%d", userID)
	if err := r.client.Del(ctx, keyPartner).Err(); err != nil {
		return fmt.Errorf("failed to delete partner mapping: %w", err)
	}

	return nil
}

func (r *ChatRepository) GetUsers(ctx context.Context) ([]int64, error) {
	key := "chat:users"
	users, err := r.client.SMembers(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get users from set: %w", err)
	}

	var userIDs []int64
	for _, user := range users {
		userIDs = append(userIDs, parseInt64(user))
	}
	return userIDs, nil
}

func (r *ChatRepository) CheckPartnerToEmpty(ctx context.Context, userID int64) (bool, error) {
	key := fmt.Sprintf("chat:partner:%d", userID)
	exists, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check partner existence: %w", err)
	}
	return exists > 0, nil
}

func parseInt64(s string) int64 {
	var id int64
	fmt.Sscanf(s, "%d", &id)
	return id
}
