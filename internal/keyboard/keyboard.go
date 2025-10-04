package keyboard

import "github.com/go-telegram/bot/models"

type Keyboard struct {
	rows [][]models.InlineKeyboardButton
}

func NewKeyboard() *Keyboard {
	return &Keyboard{
		rows: make([][]models.InlineKeyboardButton, 0),
	}
}

func (k *Keyboard) AddRow(buttons ...models.InlineKeyboardButton) {
	k.rows = append(k.rows, buttons)
}

func (k *Keyboard) Build() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: k.rows,
	}
}

func NewInlineButton(text, callbackData string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{
		Text:         text,
		CallbackData: callbackData,
	}
}
