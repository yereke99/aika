package handler

import (
	"aika/internal/domain"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

func (h *Handler) AdminHandler(ctx context.Context, b *bot.Bot, update *models.Update) {

	var adminId int64
	switch update.Message.From.ID {
	case h.cfg.AdminID:
		adminId = h.cfg.AdminID
	default:
		h.logger.Warn("SomeOne is trying to get admin root", zap.Any("user_id", update.Message.From.ID))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   fmt.Sprintf("SomeOne is trying to get admin root, user_id: %d", update.Message.From.ID),
		})
	}

	h.logger.Info("Admin handler", zap.Any("update", update))

	state, err := h.redisClient.GetUserState(ctx, adminId)
	if err != nil {
		h.logger.Error("Failed to get admin state from Redis", zap.Error(err))
	}
	if state != nil && state.State == stateBroadcast {
		h.SendMessage(ctx, b, update)
		return
	}

	adminKeyboard := &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{
				{Text: "📢 Хабарлама (Messages)"},
				{Text: "❌ Жабу (Close)"},
			},
		},
		ResizeKeyboard:  true,
		Selective:       true,
		OneTimeKeyboard: true,
	}

	switch update.Message.Text {
	case "/admin":
		newAdminState := &domain.UserState{
			State: stateAdminPanel,
		}
		if err := h.redisClient.SaveUserState(ctx, adminId, newAdminState); err != nil {
			h.logger.Error("Failed to save admin state to Redis", zap.Error(err))
		}
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      adminId,
			Text:        "🔧 Админ панеліне қош келдіңіз!\n\nТаңдаңыз:",
			ReplyMarkup: adminKeyboard,
		})
		if err != nil {
			h.logger.Error("Failed to send admin panel", zap.Error(err))
		}
	case "📢 Хабарлама (Messages)":
		h.handleBroadcastMenu(ctx, b, update)

	case "❌ Жабу (Close)":
		h.handleCloseAdmin(ctx, b)
	default:
		if state != nil && state.State == stateAdminPanel {
			_, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:      adminId,
				Text:        "Белгісіз команда. Төмендегі батырмаларды пайдаланыңыз:",
				ReplyMarkup: adminKeyboard,
			})
			if err != nil {
				h.logger.Error("Failed to send admin panel", zap.Error(err))
			}
		}
	}
}

func (h *Handler) SendMessage(ctx context.Context, b *bot.Bot, update *models.Update) {

	var adminId int64
	switch update.Message.From.ID {
	case h.cfg.AdminID:
		adminId = h.cfg.AdminID
	default:
		h.logger.Warn("SomeOne is trying to get admin root", zap.Any("user_id", update.Message.From.ID))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   fmt.Sprintf("SomeOne is trying to get admin root, user_id: %d", update.Message.From.ID),
		})
	}

	adminState, errRedis := h.redisClient.GetUserState(ctx, adminId)
	if errRedis != nil {
		h.logger.Error("Failed to get admin state from Redis", zap.Error(errRedis))
	}

	if adminState == nil || adminState.State != stateBroadcast {
		h.logger.Warn("Admin not in broadcast state",
			zap.String("current_state", func() string {
				if adminState == nil {
					return "nil"
				}
				return adminState.State
			}()))
		return
	}

	switch update.Message.Text {
	case "📢 Барлығына жіберу":
		h.startBroadcast(ctx, b, update, "all")
		return
	case "🛍 Клиенттерге жіберу":
		h.startBroadcast(ctx, b, update, "clients")
		return
	case "🎲 Лото қатысушыларына":
		h.startBroadcast(ctx, b, update, "loto")
		return
	case "👥 Тіркелгендерге":
		h.startBroadcast(ctx, b, update, "just")
		return
	case "🔙 Артқа (Back)":
		if err := h.redisClient.DeleteUserState(ctx, adminId); err != nil {
			h.logger.Error("Failed to delete admin state from Redis", zap.Error(err))
		}
		h.AdminHandler(ctx, b, &models.Update{
			Message: &models.Message{
				Text: "/admin",
				From: &models.User{
					ID: adminId,
				},
			},
		})
		return
	}

	if adminState.State != stateBroadcast {
		h.logger.Warn("Admin not in broadcast state", zap.String("current_state", adminState.State))
		return
	}

	broadcastType := ""
	if adminState != nil {
		broadcastType = adminState.BroadCastType
	}
	h.logger.Info("Starting broadcast", zap.String("type", broadcastType))

	msgType, fileId, caption := h.parseMessage(update.Message)

	var userIds []int64
	var err error

	switch broadcastType {
	case "all":
		userIds, err = h.userRepo.GetAllJustUserIDs(ctx)
	default:
		err = fmt.Errorf("unknown broadcast type: %s", broadcastType)
	}

	if err != nil {
		h.logger.Error("Failed to load user ids", zap.Error(err))
		_, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminId,
			Text:   fmt.Sprintf("❌ Қате: Пайдаланушы тізімін алу мүмкін болмады\n%s", err.Error()),
		})
		if sendErr != nil {
			h.logger.Error("Failed to send error message", zap.Error(sendErr))
		}
		return
	}

	if len(userIds) == 0 {
		_, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminId,
			Text:   "📭 Хабарлама жіберуге пайдаланушылар табылмады",
		})
		if sendErr != nil {
			h.logger.Error("Failed to send no users message", zap.Error(sendErr))
		}
		return
	}

	statusMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: adminId,
		Text:   fmt.Sprintf("📤 Хабарлама жіберіліп жатыр...\n👥 Жалпы: %d пайдаланушы", len(userIds)),
	})
	if err != nil {
		h.logger.Error("Failed to send status message", zap.Error(err))
		return
	}

	limiter := rate.NewLimiter(rate.Every(time.Second/30), 1)

	var wg sync.WaitGroup
	var successCount, failedCount int64
	for i := 0; i < len(userIds); i++ {
		if err := limiter.Wait(ctx); err != nil {
			h.logger.Error("Rate limiter wait error", zap.Error(err))
			break
		}
		wg.Add(1)
		go func(userId int64) {
			defer wg.Done()
			if err := h.sendToUser(ctx, b, userId, msgType, fileId, caption); err != nil {
				atomic.AddInt64(&failedCount, 1)
				h.logger.Warn("Failed to send message to user", zap.Int64("user", userId), zap.Error(err))
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(userIds[i])
	}

	wg.Wait()
	// Send final results
	finalSuccess := atomic.LoadInt64(&successCount)
	finalFailed := atomic.LoadInt64(&failedCount)
	successRate := float64(finalSuccess) / float64(len(userIds)) * 100

	finalText := fmt.Sprintf(`✅ ХАБАРЛАМА ЖІБЕРУ АЯҚТАЛДЫ!

👥 Жалпы: %d пайдаланушы
✅ Сәтті: %d
❌ Қате: %d
📊 Сәттілік: %.1f%%

📋 Хабарлама түрі: %s
⏰ Уақыт: %s`,
		len(userIds),
		finalSuccess,
		finalFailed,
		successRate,
		h.getBroadcastTypeName(broadcastType),
		time.Now().Format("2006-01-02 15:04:05"))

	if statusMsg != nil {
		b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    adminId,
			MessageID: statusMsg.ID,
			Text:      finalText,
		})
	}

	// Log broadcast results
	h.logger.Info("Broadcast completed",
		zap.String("type", broadcastType),
		zap.Int("total", len(userIds)),
		zap.Int64("success", finalSuccess),
		zap.Int64("failed", finalFailed),
		zap.Float64("success_rate", successRate))

	if err := h.redisClient.DeleteUserState(ctx, adminId); err != nil {
		h.logger.Error("Failed to delete admin state from Redis", zap.Error(err))
	}
	time.Sleep(2 * time.Second)
	h.AdminHandler(ctx, b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: adminId},
			Text: "/admin",
		},
	})
}

// Helper methods for admin panel
func (h *Handler) handleBroadcastMenu(ctx context.Context, b *bot.Bot, update *models.Update) {
	var adminId int64
	switch update.Message.From.ID {
	case h.cfg.AdminID:
		adminId = h.cfg.AdminID
	default:
		h.logger.Warn("SomeOne is trying to get admin root", zap.Any("user_id", update.Message.From.ID))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   fmt.Sprintf("SomeOne is trying to get admin root, user_id: %d", update.Message.From.ID),
		})
	}

	// Get counts for each category
	allCount, _ := h.userRepo.GetAllJustUserIDs(ctx)

	broadcastState := &domain.UserState{
		State: stateBroadcast,
	}
	if err := h.redisClient.SaveUserState(ctx, adminId, broadcastState); err != nil {
		h.logger.Error("Failed to save broadcast state to Redis", zap.Error(err))
	}

	broadcastKeyboard := &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{
				{Text: "📢 Барлығына жіберу"},
			},
		},
		ResizeKeyboard:  true,
		OneTimeKeyboard: false,
	}

	message := fmt.Sprintf(`📢 ХАБАРЛАМА ЖІБЕРУ

📊 Қол жетімді аудитория:
• 👥 Барлық пайдаланушылар: %d
• 🛍 Клиенттер: %d  
• 🎲 Лото қатысушылары: %d
• 📅 Тіркелгендер: %d

⚠️ Ескерту: Хабарлама барлық таңдалған пайдаланушыларға жіберіледі. Сақ болыңыз!

Қайсы топқа хабарлама жіберуді қалайсыз?`,
		len(allCount), len(allCount), len(allCount), len(allCount))

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      adminId,
		Text:        message,
		ReplyMarkup: broadcastKeyboard,
	})
	if err != nil {
		h.logger.Error("Failed to send broadcast menu", zap.Error(err))
	}
}

func (h *Handler) startBroadcast(ctx context.Context, b *bot.Bot, update *models.Update, broadcastType string) {
	var adminId int64
	switch update.Message.From.ID {
	case h.cfg.AdminID:
		adminId = h.cfg.AdminID
	default:
		h.logger.Warn("SomeOne is trying to get admin root", zap.Any("user_id", update.Message.From.ID))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   fmt.Sprintf("SomeOne is trying to get admin root, user_id: %d", update.Message.From.ID),
		})
	}

	// Set admin to broadcast state
	broadCastState := &domain.UserState{
		State:         stateBroadcast,
		BroadCastType: broadcastType,
	}
	if err := h.redisClient.SaveUserState(ctx, adminId, broadCastState); err != nil {
		h.logger.Error("Failed to save broadcast state to Redis", zap.Error(err))
	}

	targetDescription := h.getBroadcastTypeName(broadcastType)

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: adminId,
		Text: fmt.Sprintf(`📝 ХАБАРЛАМА ЖАЗУ

🎯 Мақсатты аудитория: %s

💡 Қолдаулатын форматтар:
• 📝 Мәтін хабарлама
• 📷 Фото + мәтін
• 🎥 Видео + мәтін  
• 📎 Файл + мәтін
• 🎵 Аудио
• 🎬 GIF анимация

Хабарламаңызды жіберіңіз:`, targetDescription),
		ReplyMarkup: &models.ReplyKeyboardMarkup{
			Keyboard: [][]models.KeyboardButton{
				{{Text: "🔙 Артқа (Back)"}},
			},
			ResizeKeyboard:  true,
			OneTimeKeyboard: false,
		},
	})
	if err != nil {
		h.logger.Error("Failed to start broadcast", zap.Error(err))
	}
}

func (h *Handler) getBroadcastTypeName(broadcastType string) string {
	switch broadcastType {
	case "all":
		return "Барлық пайдаланушылар"
	case "clients":
		return "Барлық клиенттер"
	case "loto":
		return "Лото қатысушылары"
	case "just":
		return "Тіркелген пайдаланушылар"
	default:
		return "Белгісіз"
	}
}

// sendExcelFile sends the Excel file to admin via Telegram
func (h *Handler) sendExcelFile(ctx context.Context, b *bot.Bot, update *models.Update, filePath, caption string) {
	var adminId int64
	if update.Message.From.ID == h.cfg.AdminID {
		adminId = h.cfg.AdminID
	} else {
		adminId = h.cfg.AdminID
	}
	// Check if file exists and get file info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		h.logger.Error("Failed to get file info", zap.Error(err))
		return
	}

	// Telegram has a 50MB file size limit
	if fileInfo.Size() > 50*1024*1024 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminId,
			Text:   "❌ Файл өте үлкен (>50MB). Файл жергілікті сақталды: " + filePath,
		})
		return
	}

	// Send document
	file, err := os.Open(filePath)
	if err != nil {
		h.logger.Error("Failed to open Excel file", zap.Error(err))
		return
	}
	defer file.Close()

	_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
		ChatID:   adminId,
		Document: &models.InputFileUpload{Filename: filepath.Base(filePath), Data: file},
		//Caption:  caption + "\n\n📁 Файл: " + filepath.Base(filePath) + "\n📊 Өлшемі: " + formatFileSize(fileInfo.Size()),
	})

	if err != nil {
		h.logger.Error("Failed to send Excel file", zap.Error(err), zap.String("file", filePath))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminId,
			Text:   "❌ Excel файлын жіберу мүмкін болмады. Файл жергілікті сақталды: " + filePath,
		})
	} else {
		h.logger.Info("Excel file sent successfully", zap.String("file", filePath))

		// Optional: Delete file after successful send to save space
		// Uncomment the lines below if you want to auto-delete files
		/*
			go func() {
				time.Sleep(5 * time.Minute) // Wait 5 minutes then delete
				if err := os.Remove(filePath); err != nil {
					h.logger.Warn("Failed to delete Excel file", zap.Error(err))
				}
			}()
		*/
	}
}

func (h *Handler) handleCloseAdmin(ctx context.Context, b *bot.Bot) {
	if err := h.redisClient.DeleteUserState(ctx, h.cfg.AdminID); err != nil {
		h.logger.Error("Failed to delete admin state from Redis", zap.Error(err))
	}

	// Remove keyboard
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   "✅ Админ панелі жабылды",
		ReplyMarkup: &models.ReplyKeyboardRemove{
			RemoveKeyboard: true,
		},
	})
	if err != nil {
		h.logger.Error("Failed to close admin panel", zap.Error(err))
	}
}

// sendToUser отправляет одному пользователю указанное сообщение
func (h *Handler) sendToUser(ctx context.Context, b *bot.Bot, chatID int64, msgType, fileID, caption string) error {
	switch msgType {
	case "text":
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: caption, ProtectContent: true})
		return err
	case "photo":
		_, err := b.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chatID, Photo: &models.InputFileString{Data: fileID}, Caption: caption, ProtectContent: true})
		return err
	case "video":
		_, err := b.SendVideo(ctx, &bot.SendVideoParams{ChatID: chatID, Video: &models.InputFileString{Data: fileID}, Caption: caption, ProtectContent: true})
		return err
	case "document":
		_, err := b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatID, Document: &models.InputFileString{Data: fileID}, Caption: caption, ProtectContent: true})
		return err
	case "video_note":
		_, err := b.SendVideoNote(ctx, &bot.SendVideoNoteParams{ChatID: chatID, VideoNote: &models.InputFileString{Data: fileID}, ProtectContent: true})
		return err
	case "audio":
		_, err := b.SendAudio(ctx, &bot.SendAudioParams{ChatID: chatID, Audio: &models.InputFileString{Data: fileID}, ProtectContent: true})
		return err
	default:
		return nil
	}
}

func (h *Handler) parseMessage(msg *models.Message) (msgType, fileId, caption string) {
	switch {
	case msg.Text != "":
		return "text", "", msg.Text
	case len(msg.Photo) > 0:
		return "photo", msg.Photo[len(msg.Photo)-1].FileID, msg.Caption
	case msg.Video != nil:
		return "video", msg.Video.FileID, msg.Caption
	case msg.Document != nil:
		return "document", msg.Document.FileID, msg.Caption
	case msg.VideoNote != nil:
		return "video_note", msg.VideoNote.FileID, msg.Caption
	case msg.Audio != nil:
		return "audio", msg.Audio.FileID, msg.Caption
	case msg.Location != nil:
		locationStr := fmt.Sprintf("%.6f,%.6f", msg.Location.Latitude, msg.Location.Longitude)
		return "location", "", locationStr
	case msg.Contact != nil:
		contactStr := fmt.Sprintf("%s: %s", msg.Contact.FirstName, msg.Contact.PhoneNumber)
		return "contact", "", contactStr
	default:
		return "", "", ""
	}
}
