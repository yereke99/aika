package handler

import (
	"aika/config"
	"aika/internal/domain"
	"aika/internal/keyboard"
	"aika/internal/repository"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

const (
	stateStart      string = "start"
	stateCount      string = "count"
	statePaid       string = "paid"
	stateContact    string = "contact"
	stateAdminPanel string = "admin_panel"
	stateBroadcast  string = "broadcast"
)

// ---------- API: MESSAGE ----------
type messageAPIRequest struct {
	ToUserID string `json:"to_user_id"`
	Text     string `json:"text"`
}

// generic API response used by several handlers (message, etc.)
type genericAPIResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type RegisterResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	UserId  string `json:"user_id,omitempty"`
}

type Handler struct {
	logger      *zap.Logger
	cfg         *config.Config
	bot         *bot.Bot
	ctx         context.Context
	userRepo    *repository.UserRepository
	redisClient *repository.ChatRepository
}

func NewHandler(logger *zap.Logger, cfg *config.Config, ctx context.Context, db *sql.DB, redisClient *repository.ChatRepository) *Handler {
	return &Handler{
		logger:      logger,
		cfg:         cfg,
		ctx:         ctx,
		userRepo:    repository.NewUserRepository(db),
		redisClient: redisClient,
	}
}

func (h *Handler) getOrCreateUserState(ctx context.Context, userID int64) *domain.UserState {
	state, err := h.redisClient.GetUserState(ctx, userID)
	if err != nil {
		h.logger.Error("Redis error, using fallback state",
			zap.Error(err),
			zap.Int64("user_id", userID))

		// Return a safe default state
		return &domain.UserState{
			State:  stateStart,
			Count:  0,
			IsPaid: false,
		}
	}

	if state == nil {
		state = &domain.UserState{
			State:  stateStart,
			Count:  0,
			IsPaid: false,
		}

		// Try to save, but don't fail if Redis is down
		if err := h.redisClient.SaveUserState(ctx, userID, state); err != nil {
			h.logger.Warn("Failed to save state to Redis, continuing with in-memory state",
				zap.Error(err))
		}
	}
	return state
}

func (h *Handler) SetBot(b *bot.Bot) { h.bot = b }

func (h *Handler) DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	userId := update.Message.From.ID

	ok, errE := h.userRepo.ExistsJust(ctx, userId)
	if errE != nil {
		h.logger.Error("Failed to check user", zap.Error(errE))
	} else if !ok {
		timeNow := time.Now().Format("2006-01-02 15:04:05")
		h.logger.Info("New user", zap.String("user_id", strconv.FormatInt(userId, 10)), zap.String("date", timeNow))
		if errN := h.userRepo.InsertJust(ctx, domain.JustEntry{
			UserId:         userId,
			UserName:       update.Message.From.Username,
			DateRegistered: timeNow,
		}); errN != nil {
			h.logger.Error("Failed to insert user", zap.Error(errN))
		}
	}

	userState := h.getOrCreateUserState(ctx, userId)

	if update.CallbackQuery != nil {
		switch userState.State {
		case stateAdminPanel:
			h.AdminHandler(ctx, b, update)
		case stateBroadcast:
			h.SendMessage(ctx, b, update)
		default:
			h.DefaultHandler(ctx, b, update)
		}
		return
	}

	switch userState.State {
	case stateAdminPanel:
		h.AdminHandler(ctx, b, update)
	case stateBroadcast:
		h.SendMessage(ctx, b, update)
	default:
		h.DefaultHandler(ctx, b, update)
	}

	h.HandleChat(ctx, b, update)

	h.logger.Info("Received message",
		zap.String("text", update.Message.Text),
		zap.Int64("chatID", update.Message.Chat.ID),
	)
}

func (h *Handler) StartWebServer(ctx context.Context, b *bot.Bot) {
	h.SetBot(b)

	mux := http.NewServeMux()

	// HTML pages
	mux.HandleFunc("/logo", func(w http.ResponseWriter, r *http.Request) {
		path := "./static/logo.html"
		http.ServeFile(w, r, path)
	})
	mux.HandleFunc("/", h.WelcomePageHandler)
	mux.HandleFunc("/welcome.html", h.WelcomePageHandler)
	mux.HandleFunc("/register.html", h.RegisterPageHandler)
	mux.HandleFunc("/list.html", h.ListPageHandler)
	mux.HandleFunc("/user-detail.html", h.UserDetailPageHandler)
	mux.HandleFunc("/user-update.html", h.UserUpdatePageHandler)

	// Static for uploads
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))

	// API
	mux.HandleFunc("/api/user/check", h.CheckUserHandler)
	mux.HandleFunc("/api/user/register", h.HandleRegister)
	mux.HandleFunc("/api/user/update", h.UpdateUserHandler)
	mux.HandleFunc("/api/users/nearby", h.GetNearbyUsersHandler)
	mux.HandleFunc("/api/users/", h.GetUserByIDHandler) // /api/users/{id}

	// Like and message
	mux.HandleFunc("/api/user/like", h.LikeHandler)
	mux.HandleFunc("/api/user/message", h.MessageHandler)

	handler := h.corsMiddleware(mux)

	addr := fmt.Sprintf(":%s", h.cfg.Port)
	h.logger.Info("Web server listening", zap.String("address", addr))

	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		h.logger.Info("Shutting down web server...")
		if err := server.Shutdown(context.Background()); err != nil {
			h.logger.Error("Error shutting down server", zap.Error(err))
		}
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		h.logger.Error("Web server error", zap.Error(err))
	}
}

func (h *Handler) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Telegram-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- Page Handlers
func serveHTML(w http.ResponseWriter, r *http.Request, path string, logger *zap.Logger) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		logger.Error("file not found", zap.String("path", path))
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, path)
}

func (h *Handler) WelcomePageHandler(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Serving welcome.html")
	serveHTML(w, r, filepath.Join("static", "welcome.html"), h.logger)
}

func (h *Handler) RegisterPageHandler(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Serving register.html")
	serveHTML(w, r, filepath.Join("static", "register.html"), h.logger)
}

func (h *Handler) ListPageHandler(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Serving list.html")
	serveHTML(w, r, filepath.Join("static", "list.html"), h.logger)
}

func (h *Handler) UserDetailPageHandler(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Serving user-detail.html")
	serveHTML(w, r, filepath.Join("static", "user-detail.html"), h.logger)
}

func (h *Handler) UserUpdatePageHandler(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Serving user-update.html")
	serveHTML(w, r, filepath.Join("static", "user-update.html"), h.logger)
}

// ---------- API

type CheckUserRequest struct {
	TelegramId int64  `json:"telegram_id"`
	Username   string `json:"username,omitempty"`
	FirstName  string `json:"first_name,omitempty"`
	LastName   string `json:"last_name,omitempty"`
}
type CheckUserResponse struct {
	Exists bool   `json:"exists"`
	UserId string `json:"user_id,omitempty"`
}

// ====== Ключи для контекста, чтобы не менять сигнатуры sendLike/sendMessage
type ctxKey string

const (
	ctxLikeFromKey ctxKey = "aika_like_from"
	ctxMsgFromKey  ctxKey = "aika_msg_from"
	ctxMsgTextKey  ctxKey = "aika_msg_text"
)

// ====== Утилита: достать TG ID из контекста/заголовка
func currentTGID(r *http.Request) (int64, error) {
	if v := r.Context().Value("tg_id"); v != nil {
		if id, ok := v.(int64); ok && id > 0 {
			return id, nil
		}
	}
	if h := r.Header.Get("X-Telegram-Id"); h != "" {
		var id int64
		_, err := fmt.Sscanf(h, "%d", &id)
		if err == nil {
			return id, nil
		}
	}
	return 0, errors.New("unauthorized: telegram id is missing")
}

// ====== Вспомогательные билдеры текста
func sexKZ(sex string) string {
	switch strings.ToLower(strings.TrimSpace(sex)) {
	case "male", "ер", "m":
		return "Ер адам"
	case "female", "әйел", "f":
		return "Әйел адам"
	default:
		return "—"
	}
}
func sexEmoji(sex string) string {
	switch strings.ToLower(strings.TrimSpace(sex)) {
	case "male", "m", "ер":
		return "👨"
	case "female", "f", "ж":
		return "👩"
	default:
		return "🙂"
	}
}
func safeNickKZ(nick string) string {
	n := strings.TrimSpace(nick)
	if n == "" {
		return "досым"
	}
	return n
}

// ---------- API: LIKE ----------
type likeAPIRequest struct {
	ToUserID string `json:"to_user_id"` // DB user ID (uuid/text)
}
type likeAPIResponse struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
	Delivered bool   `json:"delivered"`
}

func (h *Handler) LikeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeJSON(w, http.StatusMethodNotAllowed, likeAPIResponse{OK: false, Message: "method not allowed"})
		return
	}

	var req likeAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ToUserID) == "" {
		h.writeJSON(w, http.StatusBadRequest, likeAPIResponse{OK: false, Message: "invalid body"})
		return
	}

	fromTG, err := currentTGID(r)
	if err != nil {
		h.writeJSON(w, http.StatusUnauthorized, likeAPIResponse{OK: false, Message: "unauthorized"})
		return
	}
	fromUser, err := h.userRepo.GetUserByTelegramId(fromTG)
	if err != nil || fromUser == nil {
		h.logger.Error("like: sender not found", zap.Int64("fromTG", fromTG), zap.Error(err))
		h.writeJSON(w, http.StatusBadRequest, likeAPIResponse{OK: false, Message: "sender not found"})
		return
	}
	toUser, err := h.userRepo.GetUserByID(req.ToUserID)
	if err != nil || toUser == nil {
		h.logger.Error("like: recipient not found", zap.String("toUserID", req.ToUserID), zap.Error(err))
		h.writeJSON(w, http.StatusBadRequest, likeAPIResponse{OK: false, Message: "recipient not found"})
		return
	}
	if toUser.TelegramId == 0 {
		h.writeJSON(w, http.StatusBadRequest, likeAPIResponse{OK: false, Message: "recipient has no telegram"})
		return
	}
	if toUser.TelegramId == fromUser.TelegramId {
		h.writeJSON(w, http.StatusBadRequest, likeAPIResponse{OK: false, Message: "cannot like yourself"})
		return
	}
	if h.bot == nil {
		h.logger.Error("like: telegram bot is nil; cannot send")
		h.writeJSON(w, http.StatusInternalServerError, likeAPIResponse{OK: false, Message: "bot unavailable"})
		return
	}

	go func(from *domain.User, to *domain.User) {
		if ok := h.sendLike(context.Background(), h.bot, from, to); !ok {
			h.logger.Warn("like: delivery failed",
				zap.Int64("fromTG", from.TelegramId),
				zap.Int64("toTG", to.TelegramId),
				zap.String("toUserDBID", to.Id),
			)
		}
	}(fromUser, toUser)
	h.writeJSON(w, http.StatusOK, likeAPIResponse{OK: true, Message: "liked", Delivered: true})
}

// sendLike now takes both users explicitly and returns whether delivery happened
func (h *Handler) sendLike(ctx context.Context, b *bot.Bot, from *domain.User, to *domain.User) bool {
	if b == nil || from == nil || to == nil || to.TelegramId == 0 {
		return false
	}

	nick := safeNickKZ(from.Nickname)
	ageText := "—"
	if from.Age > 0 {
		ageText = fmt.Sprintf("%d жаста", from.Age)
	}
	about := strings.TrimSpace(from.AboutUser)
	if about == "" {
		about = "—"
	}
	const aboutLimit = 300
	if utf8.RuneCountInString(about) > aboutLimit {
		r := []rune(about)
		about = string(r[:aboutLimit]) + "…"
	}

	caption := fmt.Sprintf(
		"❤️ Сізге лайк қойды!\n\n%s\nЖынысы: %s\nЖасы: %s\n\nӨзі туралы: %s",
		sexEmoji(from.Sex)+" "+nick,
		sexKZ(from.Sex),
		ageText,
		about,
	)

	if p := strings.TrimSpace(from.AvatarPath); p != "" {
		if f, err := os.Open(p); err != nil {
			h.logger.Warn("like: open avatar failed", zap.String("path", p), zap.Error(err))
		} else {
			defer f.Close()
			ctxPhoto, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			kb := keyboard.NewKeyboard()
			kb.AddRow(keyboard.NewInlineButton("💬 Сөйлесуді бастау", fmt.Sprintf("select_%d", from.TelegramId)))
			_, err := b.SendPhoto(ctxPhoto, &bot.SendPhotoParams{
				ChatID:         to.TelegramId,
				Photo:          &models.InputFileUpload{Data: f, Filename: filepath.Base(p)},
				Caption:        caption,    // optional but good
				ReplyMarkup:    kb.Build(), // <- no helper involved
				ProtectContent: true,
			})
			if err == nil {
				return true
			}
			h.logger.Error("like: sendPhoto failed", zap.Error(err))
		}
	}

	// 2) Fallback: plain text with a fresh timeout
	ctxMsg, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := b.SendMessage(ctxMsg, &bot.SendMessageParams{
		ChatID:         to.TelegramId,
		Text:           caption,
		ProtectContent: true,
	})
	if err != nil {
		h.logger.Error("like: sendMessage failed", zap.Error(err))
		return false
	}
	return true
}

func (h *Handler) MessageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeJSON(w, http.StatusMethodNotAllowed, genericAPIResponse{OK: false, Message: "method not allowed"})
		return
	}
	var req messageAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ToUserID) == "" {
		h.writeJSON(w, http.StatusBadRequest, genericAPIResponse{OK: false, Message: "invalid body"})
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		h.writeJSON(w, http.StatusBadRequest, genericAPIResponse{OK: false, Message: "empty message"})
		return
	}

	fromTG, err := currentTGID(r)
	if err != nil {
		h.writeJSON(w, http.StatusUnauthorized, genericAPIResponse{OK: false, Message: "unauthorized"})
		return
	}

	fromUser, err := h.userRepo.GetUserByTelegramId(fromTG)
	if err != nil || fromUser == nil {
		h.logger.Error("sender not found", zap.Error(err))
		h.writeJSON(w, http.StatusBadRequest, genericAPIResponse{OK: false, Message: "sender not found"})
		return
	}
	toUser, err := h.userRepo.GetUserByID(req.ToUserID)
	if err != nil || toUser == nil {
		h.logger.Error("recipient not found", zap.Error(err))
		h.writeJSON(w, http.StatusBadRequest, genericAPIResponse{OK: false, Message: "recipient not found"})
		return
	}
	if toUser.TelegramId == 0 {
		h.writeJSON(w, http.StatusBadRequest, genericAPIResponse{OK: false, Message: "recipient has no telegram"})
		return
	}

	// Можно сохранить в БД (если есть метод репозитория).
	// _ = h.userRepo.InsertMessage(fromUser.Id, toUser.Id, req.Text)

	// Передаём данные в контекст → шаблонная функция
	bg := context.WithValue(context.Background(), ctxMsgFromKey, fromUser)
	bg = context.WithValue(bg, ctxMsgTextKey, req.Text)
	ctxSend, cancel := context.WithTimeout(bg, 15*time.Second)
	go func() {
		defer cancel()
		h.sendMessage(ctxSend, h.bot, toUser)
	}()

	h.writeJSON(w, http.StatusOK, genericAPIResponse{OK: true, Message: "sent"})
}

// Реализация шаблонной функции: отправка сообщения с подписью, кто пишет
func (h *Handler) sendMessage(ctx context.Context, b *bot.Bot, user *domain.User) {
	if b == nil || user == nil || user.TelegramId == 0 {
		return
	}
	fromUser, _ := ctx.Value(ctxMsgFromKey).(*domain.User)
	text, _ := ctx.Value(ctxMsgTextKey).(string)
	if fromUser == nil || strings.TrimSpace(text) == "" {
		return
	}

	nick := safeNickKZ(fromUser.Nickname)
	header := fmt.Sprintf("💬 Жаңа хабарлама %s:", nick)
	out := header + "\n\n" + text

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:         user.TelegramId,
		Text:           out,
		ProtectContent: true,
	}); err != nil {
		h.logger.Error("send message failed", zap.Error(err))
	}
}

func (h *Handler) CheckUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req CheckUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Failed to decode request", zap.Error(err))
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	exists, err := h.userRepo.CheckUserExists(req.TelegramId)
	if err != nil {
		h.logger.Error("Failed to check user", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	var userId string
	if exists {
		user, err := h.userRepo.GetUserByTelegramId(req.TelegramId)
		if err == nil && user != nil {
			userId = user.Id
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CheckUserResponse{Exists: exists, UserId: userId})
}

func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "Invalid form data"})
		return
	}

	telegramIDStr := r.FormValue("telegram_id")
	nickname := r.FormValue("nickname")
	sex := r.FormValue("sex")
	ageStr := r.FormValue("age")
	latitudeStr := r.FormValue("latitude")
	longitudeStr := r.FormValue("longitude")
	aboutUser := r.FormValue("about_user")

	if telegramIDStr == "" || nickname == "" || sex == "" || ageStr == "" {
		h.writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "Missing required fields"})
		return
	}

	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "Invalid telegram_id"})
		return
	}
	age, err := strconv.Atoi(ageStr)
	if err != nil || age < 18 {
		h.writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "Invalid age (must be 18+)"})
		return
	}
	latitude, err := strconv.ParseFloat(latitudeStr, 64)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "Invalid latitude"})
		return
	}
	longitude, err := strconv.ParseFloat(longitudeStr, 64)
	if err != nil {
		h.writeJSON(w, http.StatusBadRequest, RegisterResponse{Success: false, Error: "Invalid longitude"})
		return
	}

	avatarPath := ""
	if file, header, err := r.FormFile("avatar"); err == nil {
		defer file.Close()
		_ = os.MkdirAll("uploads/avatars", 0755)
		avatarPath = filepath.Join("uploads/avatars", fmt.Sprintf("%d_%d_%s", telegramID, time.Now().Unix(), sanitizeFilename(header.Filename)))
		if dst, err := os.Create(avatarPath); err == nil {
			defer dst.Close()
			_, _ = io.Copy(dst, file)
		} else {
			avatarPath = ""
		}
	}

	user := &domain.User{
		TelegramId: telegramID,
		Nickname:   nickname,
		Sex:        sex,
		Age:        age,
		Latitude:   &latitude,
		Longitude:  &longitude,
		AboutUser:  aboutUser,
		AvatarPath: avatarPath,
	}

	userId, err := h.userRepo.CreateUser(user)
	if err != nil {
		h.writeJSON(w, http.StatusInternalServerError, RegisterResponse{Success: false, Error: "Failed to register user"})
		return
	}

	go h.sendConfirmationMessageToRegister(r.Context(), h.bot, user)

	h.writeJSON(w, http.StatusOK, RegisterResponse{Success: true, Message: "User registered successfully", UserId: userId})
}

func (h *Handler) sendConfirmationMessageToRegister(ctx context.Context, b *bot.Bot, user *domain.User) {
	if user == nil {
		return
	}

	safeNickKZ := func(nick string) string {
		n := strings.TrimSpace(nick)
		if n == "" {
			return "досым"
		}
		return n
	}
	sexKZ := func(sex string) string {
		switch strings.ToLower(strings.TrimSpace(sex)) {
		case "male", "ер", "m":
			return "Ер адам"
		case "female", "әйел", "f":
			return "Әйел адам"
		default:
			return "—"
		}
	}
	yesNoKZ := func(ok bool, yes, no string) string {
		if ok {
			return yes
		}
		return no
	}

	nick := safeNickKZ(user.Nickname)
	ageText := "—"
	if user.Age > 0 {
		ageText = fmt.Sprintf("%d", user.Age)
	}
	geoOK := (user.Latitude != nil && user.Longitude != nil)
	about := strings.TrimSpace(user.AboutUser)
	if about == "" {
		about = "—"
	}

	const aboutLimit = 300
	if utf8.RuneCountInString(about) > aboutLimit {
		r := []rune(about)
		about = string(r[:aboutLimit]) + "…"
	}

	details := fmt.Sprintf(
		"• Атыңыз (ник): %s\n"+
			"• Жасы: %s\n"+
			"• Жынысы: %s\n"+
			"• Геолокация: %s\n"+
			"• Фото: %s\n"+
			"• Telegram ID: %d\n"+
			"• Өзім туралы: %s",
		nick,
		ageText,
		sexKZ(user.Sex),
		yesNoKZ(geoOK, "✅ сақталды", "—"),
		yesNoKZ(user.AvatarPath != "", "✅ жүктелді", "—"),
		user.TelegramId,
		about,
	)

	caption := fmt.Sprintf(
		"🎉 Тіркеу сәтті өтті, %s!\n\n"+
			"%s\n\n"+
			"AIKA-ға қош келдіңіз! Енді жаныңыздағы адамдарды қарап, ұнағанына ❤️ басып, бірден сөйлесе аласыз. 👋💬\n\n"+
			"Жаңа таныстықтар мен жақсы әңгімелер тілейміз! ✨",
		nick, details,
	)

	if user.AvatarPath != "" {
		file, err := os.Open(user.AvatarPath)
		if err != nil {
			h.logger.Error("open profile photo failed", zap.Error(err))
		} else {
			defer file.Close()
			if _, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
				ChatID: user.TelegramId,
				Photo: &models.InputFileUpload{
					Filename: filepath.Base(user.AvatarPath),
					Data:     file,
				},
				Caption:        caption,
				ProtectContent: true,
			}); err == nil {
				return
			} else {
				h.logger.Error("send photo confirmation failed", zap.Error(err))
			}
		}
	}

	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:         user.TelegramId,
		Text:           caption,
		ProtectContent: true,
	}); err != nil {
		h.logger.Error("send text confirmation failed", zap.Error(err))
	}
}

// ----- Update profile (multipart form)
type UpdateResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

func (h *Handler) UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.writeJSON(w, http.StatusBadRequest, UpdateResponse{Success: false, Error: "Invalid form data"})
		return
	}

	userID := r.FormValue("user_id")
	telegramIDStr := r.FormValue("telegram_id")

	var target *domain.User
	if userID != "" {
		u, err := h.userRepo.GetUserByID(userID)
		if err != nil {
			h.writeJSON(w, http.StatusInternalServerError, UpdateResponse{Success: false, Error: "Lookup failed"})
			return
		}
		if u == nil {
			h.writeJSON(w, http.StatusNotFound, UpdateResponse{Success: false, Error: "User not found"})
			return
		}
		target = u
	} else if telegramIDStr != "" {
		tid, err := strconv.ParseInt(telegramIDStr, 10, 64)
		if err != nil {
			h.writeJSON(w, http.StatusBadRequest, UpdateResponse{Success: false, Error: "Invalid telegram_id"})
			return
		}
		u, err := h.userRepo.GetUserByTelegramId(tid)
		if err != nil {
			h.writeJSON(w, http.StatusInternalServerError, UpdateResponse{Success: false, Error: "Lookup failed"})
			return
		}
		if u == nil {
			h.writeJSON(w, http.StatusNotFound, UpdateResponse{Success: false, Error: "User not found"})
			return
		}
		target = u
	} else {
		h.writeJSON(w, http.StatusBadRequest, UpdateResponse{Success: false, Error: "Provide user_id or telegram_id"})
		return
	}

	// Optional fields
	if v := strings.TrimSpace(r.FormValue("nickname")); v != "" {
		target.Nickname = v
	}
	if v := strings.TrimSpace(r.FormValue("sex")); v == "male" || v == "female" {
		target.Sex = v
	}
	if v := strings.TrimSpace(r.FormValue("age")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 18 {
			target.Age = n
		}
	}
	if v := strings.TrimSpace(r.FormValue("about_user")); v != "" || r.FormValue("about_user") == "" {
		// allow empty to clear
		target.AboutUser = v
	}
	if v := strings.TrimSpace(r.FormValue("latitude")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			target.Latitude = &f
		}
	}
	if v := strings.TrimSpace(r.FormValue("longitude")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			target.Longitude = &f
		}
	}

	// Avatar
	if file, header, err := r.FormFile("avatar"); err == nil {
		defer file.Close()
		_ = os.MkdirAll("uploads/avatars", 0755)
		tid := target.TelegramId
		newPath := filepath.Join("uploads/avatars", fmt.Sprintf("%d_%d_%s", tid, time.Now().Unix(), sanitizeFilename(header.Filename)))
		if dst, err := os.Create(newPath); err == nil {
			defer dst.Close()
			_, _ = io.Copy(dst, file)
			target.AvatarPath = newPath
		}
	}

	if err := h.userRepo.UpdateUser(target); err != nil {
		h.writeJSON(w, http.StatusInternalServerError, UpdateResponse{Success: false, Error: "Update failed"})
		return
	}
	h.writeJSON(w, http.StatusOK, UpdateResponse{Success: true, Message: "Updated"})
}

// ----- Get by ID
func (h *Handler) GetUserByIDHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimPrefix(r.URL.Path, "/api/users/")
	if userID == "" || strings.Contains(userID, "/") {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	u, err := h.userRepo.GetUserByID(userID)
	if err != nil {
		h.logger.Error("GetUserByID failed", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	var dist float64
	if origin := r.URL.Query().Get("origin"); origin != "" && u.Latitude != nil && u.Longitude != nil {
		pp := strings.Split(origin, ",")
		if len(pp) == 2 {
			if olat, err1 := strconv.ParseFloat(strings.TrimSpace(pp[0]), 64); err1 == nil {
				if olon, err2 := strconv.ParseFloat(strings.TrimSpace(pp[1]), 64); err2 == nil {
					dist = haversineKm(olat, olon, *u.Latitude, *u.Longitude)
				}
			}
		}
	}

	type response struct {
		ID         string  `json:"id"`
		UserID     int64   `json:"user_id"`
		Nickname   string  `json:"nickname"`
		Sex        string  `json:"sex"`
		Age        int     `json:"age"`
		Latitude   float64 `json:"latitude,omitempty"`
		Longitude  float64 `json:"longitude,omitempty"`
		AboutUser  string  `json:"about_user,omitempty"`
		AvatarPath string  `json:"avatar_path,omitempty"`
		AvatarURL  string  `json:"avatar_url,omitempty"`
		DistanceKm float64 `json:"distance_km,omitempty"`
	}

	var lat, lon float64
	if u.Latitude != nil {
		lat = *u.Latitude
	}
	if u.Longitude != nil {
		lon = *u.Longitude
	}

	avatarURL := makeAvatarURL(u.AvatarPath)
	out := response{
		ID:         u.Id,
		UserID:     u.TelegramId,
		Nickname:   u.Nickname,
		Sex:        u.Sex,
		Age:        u.Age,
		Latitude:   lat,
		Longitude:  lon,
		AboutUser:  u.AboutUser,
		AvatarPath: u.AvatarPath,
		AvatarURL:  avatarURL,
		DistanceKm: dist,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ----- Nearby users (+filters)
type NearbyUser struct {
	ID         string  `json:"id"`
	UserID     int64   `json:"user_id"`
	Nickname   string  `json:"nickname"`
	Sex        string  `json:"sex"`
	Age        int     `json:"age"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	AboutUser  string  `json:"about_user,omitempty"`
	AvatarPath string  `json:"avatar_path,omitempty"`
	AvatarURL  string  `json:"avatar_url,omitempty"`
	DistanceKm float64 `json:"distance_km"`
}

func (h *Handler) GetNearbyUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	loc := q.Get("location")
	var lat, lon float64
	if loc != "" {
		parts := strings.Split(loc, ",")
		if len(parts) == 2 {
			latParsed, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			lonParsed, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil {
				lat, lon = latParsed, lonParsed
			}
		}
	}

	radiusKm := 50.0
	if v, err := parseFloatParam(q, "radius_km"); err == nil && v != nil && *v > 0 && *v <= 300 {
		radiusKm = *v
	}

	sex := q.Get("sex")
	if sex != "" && sex != "male" && sex != "female" {
		sex = ""
	}

	ageMinPtr, _ := parseIntParam(q, "age_min")
	ageMaxPtr, _ := parseIntParam(q, "age_max")

	search := strings.TrimSpace(q.Get("q"))

	limit := 50
	if lPtr, _ := parseIntParam(q, "limit"); lPtr != nil && *lPtr > 0 && *lPtr <= 100 {
		limit = *lPtr
	}

	// fetch candidates
	var users []domain.User
	var err error
	if loc == "" {
		users, err = h.userRepo.FindUsersByFilters(sex, ageMinPtr, ageMaxPtr, search, limit)
	} else {
		latMin, latMax, lonMin, lonMax := bboxFromPoint(lat, lon, radiusKm)
		users, err = h.userRepo.FindUsersInBBox(latMin, latMax, lonMin, lonMax, sex, ageMinPtr, ageMaxPtr, search, limit*3)
	}
	if err != nil {
		h.logger.Error("repo nearby failed", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	out := make([]NearbyUser, 0, len(users))
	for _, u := range users {
		var d float64
		if loc != "" && u.Latitude != nil && u.Longitude != nil {
			d = haversineKm(lat, lon, *u.Latitude, *u.Longitude)
			if d > radiusKm {
				continue
			}
		}
		out = append(out, NearbyUser{
			ID:         u.Id,
			UserID:     u.TelegramId,
			Nickname:   u.Nickname,
			Sex:        u.Sex,
			Age:        u.Age,
			Latitude:   derefOrZero(u.Latitude),
			Longitude:  derefOrZero(u.Longitude),
			AboutUser:  u.AboutUser,
			AvatarPath: u.AvatarPath,
			AvatarURL:  makeAvatarURL(u.AvatarPath),
			DistanceKm: d,
		})
	}

	if loc != "" {
		sort.Slice(out, func(i, j int) bool { return out[i].DistanceKm < out[j].DistanceKm })
	}
	if len(out) > limit {
		out = out[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ---------- Helpers
func parseFloatParam(q url.Values, key string) (*float64, error) {
	s := q.Get(key)
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}
func parseIntParam(q url.Values, key string) (*int, error) {
	s := q.Get(key)
	if s == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
func bboxFromPoint(lat, lon, radiusKm float64) (latMin, latMax, lonMin, lonMax float64) {
	latDelta := radiusKm / 111.0
	lonDelta := radiusKm / (111.0 * math.Cos(lat*math.Pi/180))
	return lat - latDelta, lat + latDelta, lon - lonDelta, lon + lonDelta
}
func derefOrZero(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func makeAvatarURL(path string) string {
	if path == "" {
		return ""
	}
	// store as /uploads/...
	if strings.HasPrefix(path, "uploads/") {
		return "/" + path
	}
	return "/uploads/" + filepath.Base(path)
}

func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "..", "_")
	return s
}
