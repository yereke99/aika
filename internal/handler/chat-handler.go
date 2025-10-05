package handler

import (
	"aika/internal/keyboard"
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

func (h *Handler) InlineHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}

	data := update.CallbackQuery.Data
	fmt.Println(data)
	if !strings.HasPrefix(data, "select_") {
		return
	}

	idStr := strings.TrimPrefix(data, "select_")
	selectedId, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.logger.Error("parse selected id", zap.String("data", data), zap.Error(err))
		return
	}

	fmt.Println("id: ", selectedId)

	ok, err := h.redisClient.CheckPartnerToEmpty(ctx, selectedId)
	if err != nil {
		h.logger.Error("error in check partner", zap.Error(err))
		return
	}
	if ok {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.CallbackQuery.From.ID,
			Text:   fmt.Sprintf("Қолданушы қазір бос емес, күте тұрыңыз: %d", selectedId),
		})
		return
	}

	if err := h.redisClient.SetPartner(ctx, update.CallbackQuery.From.ID, selectedId); err != nil {
		h.logger.Error("error in set partner", zap.Error(err))
		return
	}

	if err := h.redisClient.SetPartner(ctx, selectedId, update.CallbackQuery.From.ID); err != nil {
		h.logger.Error("error in set partner", zap.Error(err))
		return
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.CallbackQuery.From.ID,
		Text:   fmt.Sprintf("Сіз сұхбаттасушыға ID арқылы қосылдыңыз: %d\nБұл чатта(боттың ішінде) барлық типтегі хабарламалар(📷 Фото, 🎥 Видео, 🔊 Аудио, 📍 Геолокация, 📄 Құжат, ❓ Сұрақтар) жіберуге болады! Жай ғана сәлем немесе фото видео жіберсеңіз болады 😉", selectedId),
	})
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: selectedId,
		Text:   fmt.Sprintf("Сіз сұхбаттасушыға ID арқылы қосылдыңыз: %d\nБұл чатта(боттың ішінде) барлық типтегі хабарламалар(📷 Фото, 🎥 Видео, 🔊 Аудио, 📍 Геолокация, 📄 Құжат, ❓ Сұрақтар) жіберуге болады! Жай ғана сәлем немесе фото видео жіберсеңіз болады 😉", update.CallbackQuery.From.ID),
	})
}

// CallbackHandlerExit обрабатывает выход пользователя из чата.
func (h *Handler) CallbackHandlerExit(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := update.CallbackQuery.From.ID
	partnerID, err := h.redisClient.GetUserPartner(ctx, userID)
	if err != nil {
		fmt.Println("Ошибка при получении собеседника:", err)
		return
	}

	if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
		fmt.Println("Ошибка при удалении пользователя:", err)
		return
	}

	if partnerID != 0 {
		if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
			fmt.Println("Ошибка при удалении собеседника:", err)
			return
		}
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: partnerID,
			Text:   "Сіздің партнер-(-ша) чаттан шықты.",
		})
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      userID,
		Text:        "Сіз чаттан шықтыңыз",
		ReplyMarkup: nil,
	})
}

func (h *Handler) HandleChat(ctx context.Context, b *bot.Bot, update *models.Update) {
	userID := update.Message.From.ID
	partnerID, err := h.redisClient.GetUserPartner(ctx, userID)
	if err != nil {
		h.logger.Error("error get user partner", zap.Error(err))
	}
    

	if partnerID == 0 {
		kb := keyboard.NewKeyboard()
	    kb.AddRow(keyboard.NewWebAppButton("🚀 AIKA Mini App", h.cfg.MiniAppURL))

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      update.Message.Chat.ID,
			Text:        "Чатқа қосылу үшін төмендегі 🚀 AIKA Mini App батырмасын басыңыз.",
			ReplyMarkup: kb.Build(),
		})
	return
	}

	senderNickname, err := h.userRepo.GetUserNickname(userID)
	if err != nil && senderNickname == "" {
		senderNickname = update.Message.From.Username
	}

	partnerIdentifier := fmt.Sprintf("%d", partnerID)
	kb := keyboard.NewKeyboard()
	kb.AddRow(keyboard.NewInlineButton("🔕 Шығу", "exit"))

	switch {
	case update.Message.Text != "":
		fmt.Printf("TEXT | User=%s | Text=%q\n", senderNickname, update.Message.Text)

		partnerMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         partnerID,
			Text:           fmt.Sprintf("от %s: %s", senderNickname, update.Message.Text),
			ParseMode:      "HTML",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
		}

		senderMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         update.Message.Chat.ID,
			Text:           "Егер хабарламаны өшіргіңіз келсе, төмендегі батырманы басыңыз.",
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка отправки текстового сообщения отправителю:", err)
			return
		}

		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.From.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Хабарламыны жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))

		_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			Text:        "Егер хабарламаны өшіргіңіз келсе, төмендегі батырманы басыңыз.",
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования текстового сообщения:", err)
		}

		textToChannel := fmt.Sprintf("Сообщение от %s: к %s:\n%s", senderNickname, partnerIdentifier, update.Message.Text)
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         h.cfg.ChannelName,
			Text:           textToChannel,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки текстового сообщения:", err)
		}
	// 2. Фото.
	case update.Message.Photo != nil:
		fmt.Printf("PHOTO | User=%s | FileID=%s | Caption=%q\n", senderNickname, update.Message.Photo[len(update.Message.Photo)-1].FileID, update.Message.Caption)
		photoID := update.Message.Photo[len(update.Message.Photo)-1].FileID

		var partnerPhotoCaption string
		if update.Message.Caption == "" {
			partnerPhotoCaption = fmt.Sprintf("от %s: фото", senderNickname)
		} else {
			partnerPhotoCaption = fmt.Sprintf("от %s: %s", senderNickname, update.Message.Caption)
		}

		partnerMsg, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:         partnerID,
			Photo:          &models.InputFileString{Data: photoID},
			Caption:        partnerPhotoCaption,
			ParseMode:      "HTML",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки фото сообщения собеседнику", zap.Error(err))
			return
		}

		senderMsg, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:         update.Message.Chat.ID,
			Photo:          &models.InputFileString{Data: photoID},
			Caption:        "Егер хабарламаны өшіргіңіз келсе, төмендегі батырманы басыңыз.",
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке фото отправителю:", err)
			return
		}

		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Фотоны жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))

		_, err = b.EditMessageCaption(ctx, &bot.EditMessageCaptionParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			Caption:     "Егер хабарламаны өшіргіңіз келсе, төмендегі батырманы басыңыз.",
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования фото сообщения:", err)
		}

		var photoCaptionChannel string
		if update.Message.Caption == "" {
			photoCaptionChannel = "фото"
		} else {
			photoCaptionChannel = update.Message.Caption
		}
		captionToChannel := fmt.Sprintf("Сообщение от %s: к %s:\n%s", senderNickname, partnerIdentifier, photoCaptionChannel)
		_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:         h.cfg.ChannelName,
			Photo:          &models.InputFileString{Data: photoID},
			Caption:        captionToChannel,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки фото:", err)
		}

	// 3. Видео.
	case update.Message.Video != nil:
		fmt.Printf("VIDEO | User=%s | FileID=%s | Caption=%q\n", senderNickname, update.Message.Video.FileID, update.Message.Caption)
		var partnerVideoCaption string
		if update.Message.Caption == "" {
			partnerVideoCaption = fmt.Sprintf("от %s: видео", senderNickname)
		} else {
			partnerVideoCaption = fmt.Sprintf("от %s: %s", senderNickname, update.Message.Caption)
		}
		partnerMsg, err := b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:         partnerID,
			Video:          &models.InputFileString{Data: update.Message.Video.FileID},
			Caption:        partnerVideoCaption,
			ParseMode:      "HTML",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки видео сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:         update.Message.Chat.ID,
			Video:          &models.InputFileString{Data: update.Message.Video.FileID},
			Caption:        partnerVideoCaption,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке видео отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Видеоны жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageCaption(ctx, &bot.EditMessageCaptionParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			Caption:     partnerVideoCaption,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования видео сообщения:", err)
		}
		captionToChannel := fmt.Sprintf("Сообщение от %s: к %s:\n%s", senderNickname, partnerIdentifier, partnerVideoCaption)
		_, err = b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:         h.cfg.ChannelName,
			Video:          &models.InputFileString{Data: update.Message.Video.FileID},
			Caption:        captionToChannel,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки видео:", err)
		}

	// 4. Голосовое сообщение.
	case update.Message.Voice != nil:
		fmt.Printf("VOICE | User=%s | FileID=%s | Caption=%q\n", senderNickname, update.Message.Voice.FileID, update.Message.Caption)
		var partnerVoiceCaption string
		if update.Message.Caption == "" {
			partnerVoiceCaption = fmt.Sprintf("от %s: голосовое сообщение", senderNickname)
		} else {
			partnerVoiceCaption = fmt.Sprintf("от %s: %s", senderNickname, update.Message.Caption)
		}
		partnerMsg, err := b.SendVoice(ctx, &bot.SendVoiceParams{
			ChatID:         partnerID,
			Voice:          &models.InputFileString{Data: update.Message.Voice.FileID},
			Caption:        partnerVoiceCaption,
			ParseMode:      "HTML",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки голосового сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendVoice(ctx, &bot.SendVoiceParams{
			ChatID:         update.Message.Chat.ID,
			Voice:          &models.InputFileString{Data: update.Message.Voice.FileID},
			Caption:        partnerVoiceCaption,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке голосового сообщения отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Дыбыстық хабарламаны жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageCaption(ctx, &bot.EditMessageCaptionParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			Caption:     partnerVoiceCaption,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования голосового сообщения:", err)
		}
		captionToChannel := fmt.Sprintf("Сообщение от: %s к %s:\n%s", senderNickname, partnerIdentifier, partnerVoiceCaption)
		_, err = b.SendVoice(ctx, &bot.SendVoiceParams{
			ChatID:         h.cfg.ChannelName,
			Voice:          &models.InputFileString{Data: update.Message.Voice.FileID},
			Caption:        captionToChannel,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки голосового сообщения:", err)
		}

	// 5. Видео-сообщение (VideoNote).
	case update.Message.VideoNote != nil:
		fmt.Printf("VIDEO_NOTE | User=%s | FileID=%s\n", senderNickname, update.Message.VideoNote.FileID)
		// Для VideoNote поля Caption и ParseMode отсутствуют – их не указываем.
		partnerMsg, err := b.SendVideoNote(ctx, &bot.SendVideoNoteParams{
			ChatID:         partnerID,
			VideoNote:      &models.InputFileString{Data: update.Message.VideoNote.FileID},
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки видео сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendVideoNote(ctx, &bot.SendVideoNoteParams{
			ChatID:         update.Message.Chat.ID,
			VideoNote:      &models.InputFileString{Data: update.Message.VideoNote.FileID},
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке видео-сообщения отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Видео хабарламаны жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования видео-сообщения:", err)
		}
		captionToChannel := fmt.Sprintf("Сообщение от %s к %s: Видео сообщение", senderNickname, partnerIdentifier)
		_, err = b.SendVideoNote(ctx, &bot.SendVideoNoteParams{
			ChatID:         h.cfg.ChannelName,
			VideoNote:      &models.InputFileString{Data: update.Message.VideoNote.FileID},
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки видео-сообщения:", err)
		}
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         h.cfg.ChannelName,
			Text:           captionToChannel,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки текста для видео-сообщения:", err)
		}

	// 6. Документ.
	case update.Message.Document != nil:
		fmt.Printf("DOCUMENT | User=%s | FileID=%s | Caption=%q\n", senderNickname, update.Message.Document.FileID, update.Message.Caption)
		var partnerDocCaption string
		if update.Message.Caption == "" {
			partnerDocCaption = fmt.Sprintf("от %s: документ", senderNickname)
		} else {
			partnerDocCaption = fmt.Sprintf("от %s: %s", senderNickname, update.Message.Caption)
		}
		partnerMsg, err := b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:         partnerID,
			Document:       &models.InputFileString{Data: update.Message.Document.FileID},
			Caption:        partnerDocCaption,
			ParseMode:      "HTML",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки документ сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:         update.Message.Chat.ID,
			Document:       &models.InputFileString{Data: update.Message.Document.FileID},
			Caption:        partnerDocCaption,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке документа отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Құжатты жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageCaption(ctx, &bot.EditMessageCaptionParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			Caption:     partnerDocCaption,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования документа сообщения:", err)
		}
		captionToChannel := fmt.Sprintf("Сообщение от %s: к %s:\n%s", senderNickname, partnerIdentifier, partnerDocCaption)
		_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:         h.cfg.ChannelName,
			Document:       &models.InputFileString{Data: update.Message.Document.FileID},
			Caption:        captionToChannel,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки документа:", err)
		}

	// 7. Аудио.
	case update.Message.Audio != nil:
		fmt.Printf("AUDIO | User=%s | FileID=%s | Caption=%q\n", senderNickname, update.Message.Audio.FileID, update.Message.Caption)
		var partnerAudioCaption string
		if update.Message.Caption == "" {
			partnerAudioCaption = fmt.Sprintf("от %s: аудио", senderNickname)
		} else {
			partnerAudioCaption = fmt.Sprintf("от %s: %s", senderNickname, update.Message.Caption)
		}
		partnerMsg, err := b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:         partnerID,
			Audio:          &models.InputFileString{Data: update.Message.Audio.FileID},
			Caption:        partnerAudioCaption,
			ParseMode:      "HTML",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки аудио сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:         update.Message.Chat.ID,
			Audio:          &models.InputFileString{Data: update.Message.Audio.FileID},
			Caption:        partnerAudioCaption,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке аудио отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Аудионы жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageCaption(ctx, &bot.EditMessageCaptionParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			Caption:     partnerAudioCaption,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования аудио сообщения:", err)
		}
		captionToChannel := fmt.Sprintf("Сообщение от %s к %s:\n%s", senderNickname, partnerIdentifier, partnerAudioCaption)
		_, err = b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:         h.cfg.ChannelName,
			Audio:          &models.InputFileString{Data: update.Message.Audio.FileID},
			Caption:        captionToChannel,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки аудио:", err)
		}

	// 8. Локация.
	case update.Message.Location != nil:
		fmt.Printf("LOCATION | User=%s | Lat=%.5f | Long=%.5f\n", senderNickname, update.Message.Location.Latitude, update.Message.Location.Longitude)
		partnerMsg, err := b.SendLocation(ctx, &bot.SendLocationParams{
			ChatID:         partnerID,
			Latitude:       update.Message.Location.Latitude,
			Longitude:      update.Message.Location.Longitude,
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки гео сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendLocation(ctx, &bot.SendLocationParams{
			ChatID:         update.Message.Chat.ID,
			Latitude:       update.Message.Location.Latitude,
			Longitude:      update.Message.Location.Longitude,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке локации отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Гео-локацияны жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования локации сообщения:", err)
		}
		locationText := fmt.Sprintf("Сообщение от %s: к %s:\nЛокация: %.5f, %.5f", senderNickname, partnerIdentifier, update.Message.Location.Latitude, update.Message.Location.Longitude)
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         h.cfg.ChannelName,
			Text:           locationText,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки локации:", err)
		}

	// 9. Стикер.
	case update.Message.Sticker != nil:
		fmt.Printf("STICKER | User=%s | FileID=%s\n", senderNickname, update.Message.Sticker.FileID)
		partnerMsg, err := b.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:         partnerID,
			Sticker:        &models.InputFileString{Data: update.Message.Sticker.FileID},
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки стикер сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:         update.Message.Chat.ID,
			Sticker:        &models.InputFileString{Data: update.Message.Sticker.FileID},
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке стикера отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Стикерді жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования стикера сообщения:", err)
		}
		_, err = b.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:         h.cfg.ChannelName,
			Sticker:        &models.InputFileString{Data: update.Message.Sticker.FileID},
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки стикера:", err)
		}
		stickerInfo := fmt.Sprintf("Сообщение от %s: к %s: Стикер", senderNickname, partnerIdentifier)
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         h.cfg.ChannelName,
			Text:           stickerInfo,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки текста для стикера:", err)
		}

	// 10. Контакт.
	case update.Message.Contact != nil:
		contact := update.Message.Contact
		contactText := fmt.Sprintf("от %s: контакт\nТел: %s\nИмя: %s %s", senderNickname, contact.PhoneNumber, contact.FirstName, contact.LastName)
		partnerMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         partnerID,
			Text:           contactText,
			ParseMode:      "HTML",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки контакт сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         update.Message.Chat.ID,
			Text:           contactText,
			ParseMode:      "HTML",
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке контакта отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Контактіні жою!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			Text:        contactText,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования контакта сообщения:", err)
		}
		channelContactText := fmt.Sprintf("Сообщение от %s к %s:\nКонтакт:\nТел: %s\nИмя: %s %s", senderNickname, partnerIdentifier, contact.PhoneNumber, contact.FirstName, contact.LastName)
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         h.cfg.ChannelName,
			Text:           channelContactText,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки контакта:", err)
		}

	// 11. Опрос.
	case update.Message.Poll != nil:
		poll := update.Message.Poll
		var partnerPollQuestion string
		if poll.Question == "" {
			partnerPollQuestion = fmt.Sprintf("от %s: опрос", senderNickname)
		} else {
			partnerPollQuestion = fmt.Sprintf("от %s: %s", senderNickname, poll.Question)
		}
		// Преобразуем poll.Options (тип []models.PollOption) в []models.InputPollOption
		var inputOptions []models.InputPollOption
		for _, opt := range poll.Options {
			inputOptions = append(inputOptions, models.InputPollOption{Text: opt.Text})
		}
		partnerMsg, err := b.SendPoll(ctx, &bot.SendPollParams{
			ChatID:         partnerID,
			Question:       partnerPollQuestion,
			Options:        inputOptions,
			ProtectContent: true,
		})
		if err != nil {
			if err.Error() == "forbidden, Forbidden: bot was blocked by the user" {
				if err := h.redisClient.RemoveUser(ctx, userID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				if err := h.redisClient.RemoveUser(ctx, partnerID); err != nil {
					h.logger.Error("Ошибка при удалении пользователя", zap.Error(err))
					return
				}
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: userID,
					Text:   "Қолданушы ботты бұғаттады, хабарлама жіберу мүмкін болмады басқа қолдуншылармен сөйлесіңіз!",
				})
			}
			h.logger.Error("Ошибка отправки опрос сообщения собеседнику", zap.Error(err))
			return
		}
		senderMsg, err := b.SendPoll(ctx, &bot.SendPollParams{
			ChatID:         update.Message.Chat.ID,
			Question:       poll.Question,
			Options:        inputOptions,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка при отправке опроса отправителю:", err)
			return
		}
		callbackData := fmt.Sprintf("delete_%d_%d_%d_%d", update.Message.Chat.ID, senderMsg.ID, partnerID, partnerMsg.ID)
		deleteKb := keyboard.NewKeyboard()
		deleteKb.AddRow(keyboard.NewInlineButton("⛔️ Хабарламыны жою опрос!", callbackData))
		deleteKb.AddRow(keyboard.NewInlineButton("🔕 Чатты аяқтау", "exit"))
		_, err = b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID:      update.Message.Chat.ID,
			MessageID:   senderMsg.ID,
			ReplyMarkup: deleteKb.Build(),
		})
		if err != nil {
			log.Println("Ошибка редактирования опроса сообщения:", err)
		}
		pollText := fmt.Sprintf("Сообщение от %s: к %s: Опрос\nВопрос: %s", senderNickname, partnerIdentifier, poll.Question)
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         h.cfg.ChannelName,
			Text:           pollText,
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка пересылки опроса:", err)
		}

	// 12. Неизвестный тип сообщения.
	default:
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         update.Message.Chat.ID,
			Text:           "Неизвестный тип сообщения. Попробуйте отправить текст, фото, видео, голосовое сообщение или документ.",
			ReplyMarkup:    kb.Build(),
			ProtectContent: true,
		})
		if err != nil {
			log.Println("Ошибка отправки сообщения об неизвестном типе:", err)
		}
	}
}

func (h *Handler) DeleteMessageHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	var senderChatID int64
	var senderMsgID int
	var partnerChatID int64
	var partnerMsgID int

	_, err := fmt.Sscanf(update.CallbackQuery.Data, "delete_%d_%d_%d_%d", &senderChatID, &senderMsgID, &partnerChatID, &partnerMsgID)
	if err != nil {
		fmt.Println("Ошибка при извлечении данных из callback:", err)
		return
	}

	okSend, errSender := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    senderChatID,
		MessageID: senderMsgID,
	})
	if errSender != nil {
		fmt.Println("Ошибка при удалении сообщения отправителя:", errSender)
	}

	okPartner, errPartner := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
		ChatID:    partnerChatID,
		MessageID: partnerMsgID,
	})
	if errPartner != nil {
		fmt.Println("Ошибка при удалении сообщения собеседника:", errPartner)
	}

	responseChatId := update.CallbackQuery.From.ID
	if !okSend || !okPartner {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: responseChatId,
			Text:   "Хабарлама өшірілмеді!",
		})
		return
	}
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: responseChatId,
		Text:   "Хабарлама сәтті өшірілді!",
	})
}
