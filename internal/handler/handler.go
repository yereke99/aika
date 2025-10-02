package handler

import (
	"aika/config"
	"aika/internal/domain"
	"aika/internal/repository"
	"context"
	"database/sql"
	"encoding/json"
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

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

type Handler struct {
	logger   *zap.Logger
	cfg      *config.Config
	bot      *bot.Bot
	ctx      context.Context
	userRepo *repository.UserRepository
}

func NewHandler(logger *zap.Logger, cfg *config.Config, ctx context.Context, db *sql.DB) *Handler {
	return &Handler{
		logger:   logger,
		cfg:      cfg,
		ctx:      ctx,
		userRepo: repository.NewUserRepository(db),
	}
}

func (h *Handler) SetBot(b *bot.Bot) { h.bot = b }

func (h *Handler) DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	h.logger.Info("Received message",
		zap.String("text", update.Message.Text),
		zap.Int64("chatID", update.Message.Chat.ID),
	)
}

func (h *Handler) StartWebServer(ctx context.Context, b *bot.Bot) {
	h.SetBot(b)

	mux := http.NewServeMux()

	// HTML pages
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
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

type RegisterResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	UserId  string `json:"user_id,omitempty"`
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
	h.writeJSON(w, http.StatusOK, RegisterResponse{Success: true, Message: "User registered successfully", UserId: userId})
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
