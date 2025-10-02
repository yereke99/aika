package domain

import "time"

type User struct {
	Id         string
	TelegramId int64
	Nickname   string
	Sex        string
	Age        int
	Latitude   *float64
	Longitude  *float64
	AboutUser  string
	AvatarPath string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
