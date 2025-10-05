package handler

import (
	"context"
	"fmt"
	"math/rand"
	"meily/internal/domain"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/xuri/excelize/v2"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// Add Performance Handler for admins
func (h *Handler) PerformanceHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	userID := update.Message.From.ID

	// Check if user is admin
	if userID != h.cfg.AdminID && userID != h.cfg.AdminID2 && userID != h.cfg.AdminID3 {
		return
	}

	// Get system stats
	systemStats, err := h.getSystemStats(ctx)
	if err != nil {
		h.logger.Error("Failed to get system stats", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: userID,
			Text:   "❌ Ошибка получения статистики системы",
		})
		return
	}

	// Get performance metrics from Redis
	metrics, err := h.redisRepo.GetPerformanceStats(ctx)
	if err != nil {
		h.logger.Error("Failed to get performance metrics", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: userID,
			Text:   "❌ Ошибка получения метрик производительности",
		})
		return
	}

	// Format performance report
	report := h.formatPerformanceReport(systemStats, metrics)

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    userID,
		Text:      report,
		ParseMode: "HTML",
	})
	if err != nil {
		h.logger.Error("Failed to send performance report", zap.Error(err))
	}
}

// Helper method to get system statistics
func (h *Handler) getSystemStats(ctx context.Context) (*domain.SystemStats, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Get CPU usage (simplified)
	cpuUsage := h.getCPUUsage()

	// Get uploads per second for last 10 seconds
	uploadsPerSecond, err := h.redisRepo.GetUploadsPerSecond(ctx, 10)
	if err != nil {
		h.logger.Warn("Failed to get uploads per second", zap.Error(err))
		uploadsPerSecond = 0
	}

	// Get last minute uploads
	lastMinuteUploads, err := h.redisRepo.GetLastMinuteUploads(ctx)
	if err != nil {
		h.logger.Warn("Failed to get last minute uploads", zap.Error(err))
		lastMinuteUploads = 0
	}

	return &domain.SystemStats{
		CPUUsage:          cpuUsage,
		MemoryUsage:       float64(m.Alloc) / 1024 / 1024, // MB
		GoroutineCount:    runtime.NumGoroutine(),
		UploadRate:        uploadsPerSecond,
		LastMinuteUploads: lastMinuteUploads,
	}, nil
}

// Simple CPU usage calculation
func (h *Handler) getCPUUsage() float64 {
	// This is a simplified CPU usage calculation
	// For production, consider using a proper CPU monitoring library
	var rusage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &rusage)

	// Convert to percentage (simplified)
	userTime := float64(rusage.Utime.Sec) + float64(rusage.Utime.Usec)/1000000
	sysTime := float64(rusage.Stime.Sec) + float64(rusage.Stime.Usec)/1000000

	return (userTime + sysTime) * 10 // Rough approximation
}

// Format performance report for admin
func (h *Handler) formatPerformanceReport(stats *domain.SystemStats, metrics *domain.PerformanceMetrics) string {
	return fmt.Sprintf(`
🚀 <b>СИСТЕМА ӨНІМДІЛІГІ</b> 🚀

📊 <b>ЖҮКТЕУ СТАТИСТИКАСЫ:</b>
🔸 PDF жүктеулер/секунд: <b>%.2f</b>
🔸 Соңғы минутта: <b>%d</b> жүктеу
🔸 Қазіргі секундта: <b>%d</b> жүктеу

💻 <b>ЖҮЙЕ РЕСУРСТАРЫ:</b>
🔸 CPU пайдалану: <b>%.1f%%</b>
🔸 RAM пайдалану: <b>%.1f MB</b>
🔸 Goroutine саны: <b>%d</b>

📈 <b>ҚЫЗМЕТ КӨРСЕТУ:</b>
🔸 Жалпы сұраулар: <b>%d</b>
🔸 Белсенді пайдаланушылар: <b>%d</b>
🔸 Қателер саны: <b>%d</b>
🔸 Жауап беру уақыты: <b>%d ms</b>

⏰ <b>Соңғы жаңарту:</b> %s

📋 <b>ӨНІМДІЛІК БАҒАЛАУ:</b>
%s

💡 <b>ҰСЫНЫМДАР:</b>
%s
`,
		stats.UploadRate,
		stats.LastMinuteUploads,
		metrics.DocumentUploads,
		stats.CPUUsage,
		stats.MemoryUsage,
		stats.GoroutineCount,
		metrics.TotalRequests,
		metrics.ActiveUsers,
		metrics.ErrorCount,
		metrics.ResponseTime,
		metrics.Timestamp.Format("2006-01-02 15:04:05"),
		h.getPerformanceStatus(stats, metrics),
		h.getPerformanceRecommendations(stats, metrics),
	)
}

// Get performance status based on metrics
func (h *Handler) getPerformanceStatus(stats *domain.SystemStats, metrics *domain.PerformanceMetrics) string {
	if stats.UploadRate > 5.0 {
		return "🔴 <b>ЖОҒАРЫ ЖҮКТЕМЕ</b> - Жүйе қысымда!"
	} else if stats.UploadRate > 2.0 {
		return "🟡 <b>ОРТАША ЖҮКТЕМЕ</b> - Қалыпты жұмыс"
	} else {
		return "🟢 <b>ТӨМЕН ЖҮКТЕМЕ</b> - Барлығы жақсы!"
	}
}

// Get performance recommendations
func (h *Handler) getPerformanceRecommendations(stats *domain.SystemStats, metrics *domain.PerformanceMetrics) string {
	recommendations := []string{}

	if stats.CPUUsage > 80 {
		recommendations = append(recommendations, "🔸 CPU жүктемесін азайту керек")
	}

	if stats.MemoryUsage > 500 {
		recommendations = append(recommendations, "🔸 Жады пайдалануын оңтайландыру керек")
	}

	if stats.UploadRate > 3.0 {
		recommendations = append(recommendations, "🔸 Файл өңдеуді параллельдеу керек")
	}

	if metrics.ErrorCount > 5 {
		recommendations = append(recommendations, "🔸 Қателерді тексеру керек")
	}

	if len(recommendations) == 0 {
		return "✅ Жүйе тұрақты жұмыс істеп тұр"
	}

	return strings.Join(recommendations, "\n")
}

func (h *Handler) AdminHandler(ctx context.Context, b *bot.Bot, update *models.Update) {

	var adminId int64
	switch update.Message.From.ID {
	case h.cfg.AdminID:
		adminId = h.cfg.AdminID
	case h.cfg.AdminID2:
		adminId = h.cfg.AdminID2
	case h.cfg.AdminID3:
		adminId = h.cfg.AdminID3
	default:
		h.logger.Warn("SomeOne is trying to get admin root", zap.Any("user_id", update.Message.From.ID))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   fmt.Sprintf("SomeOne is trying to get admin root, user_id: %d", update.Message.From.ID),
		})
	}

	h.logger.Info("Admin handler", zap.Any("update", update))

	state, err := h.redisRepo.GetUserState(ctx, adminId)
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
				{Text: "💰 Ақша (Money)"},
				{Text: "👥 Тіркелгендер (Just Clicked)"},
			},
			{
				{Text: "🛍 Клиенттер (Clients)"},
				{Text: "🎲 Лото (Loto)"},
			},
			{
				{Text: "📢 Хабарлама (Messages)"},
				{Text: "🎁 Сыйлық (Gift)"},
			},
			{
				{Text: "📊 Статистика (Statistics)"},
				{Text: "🚀 Қуатылық (Performance)"},
			},
			{
				{Text: "Orders"},
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
		if err := h.redisRepo.SaveUserState(ctx, adminId, newAdminState); err != nil {
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
	case "💰 Ақша (Money)":
		h.handleMoneyStats(ctx, b)

	case "👥 Тіркелгендер (Just Clicked)":
		h.handleJustUsers(ctx, b, update)

	case "🛍 Клиенттер (Clients)":
		h.handleClients(ctx, b, update)

	case "Orders":
		h.Orders(ctx, b, update)

	case "🎲 Лото (Loto)":
		h.handleLoto(ctx, b, update)

	case "📢 Хабарлама (Messages)":
		h.handleBroadcastMenu(ctx, b, update)

	case "🚀 Қуатылық (Performance)":
		h.PerformanceHandler(ctx, b, update)

	case "🎁 Сыйлық (Gift)":
		h.handleGift(ctx, b)

	case "📊 Статистика (Statistics)":
		h.handleStatistics(ctx, b)

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

func (h *Handler) Orders(ctx context.Context, b *bot.Bot, update *models.Update) {
	h.handleOrdersExcel(ctx, b, update)
}

func (h *Handler) handleOrdersExcel(ctx context.Context, b *bot.Bot, update *models.Update) {
	// 1. Fetch all orders from orders table
	orders, err := h.repo.FetchExcell(ctx)
	if err != nil {
		h.logger.Error("failed to load orders", zap.Error(err))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.From.ID,
			Text:   "❌ Қате: Тапсырыс деректерін алу мүмкін болмады",
		})
		return
	}

	if len(orders) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.From.ID,
			Text:   "📭 Ешқандай тапсырыс табылмады",
		})
		return
	}

	// 2. Prepare Excel directory
	excelDir := "./excel"
	if err := os.MkdirAll(excelDir, 0755); err != nil {
		h.logger.Error("mkdir excel failed", zap.Error(err))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.From.ID,
			Text:   "❌ Қате: Excel қалтасын жасау мүмкін болмады",
		})
		return
	}

	// 3. Create Excel file
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Orders"
	f.SetSheetName(f.GetSheetName(f.GetActiveSheetIndex()), sheet)

	// 4. Write headers
	headers := []string{
		"ID",
		"UserID",
		"UserName",
		"Quantity",
		"ФИО",
		"Contact",
		"Address",
		"DateRegister",
		"DatePay",
		"Checks",
		"Status",
	}

	for i, header := range headers {
		cell := fmt.Sprintf("%c1", 'A'+i)
		f.SetCellValue(sheet, cell, header)
	}

	// 5. Style header row
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "#FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#2563EB"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "#000000", Style: 1},
			{Type: "top", Color: "#000000", Style: 1},
			{Type: "bottom", Color: "#000000", Style: 1},
			{Type: "right", Color: "#000000", Style: 1},
		},
	})
	f.SetCellStyle(sheet, "A1", fmt.Sprintf("%c1", 'A'+len(headers)-1), headerStyle)

	// 6. Fill data with conditional formatting
	for i, order := range orders {
		row := i + 2 // Start from row 2 (after header)

		// Fill basic data
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), order.ID)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), order.UserID)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), order.UserName)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), order.Quantity)

		// Handle nullable fields
		if order.Fio.Valid {
			f.SetCellValue(sheet, fmt.Sprintf("E%d", row), order.Fio.String)
		} else {
			f.SetCellValue(sheet, fmt.Sprintf("E%d", row), "Не указано")
		}

		f.SetCellValue(sheet, fmt.Sprintf("F%d", row), order.Contact)

		if order.Address.Valid {
			f.SetCellValue(sheet, fmt.Sprintf("G%d", row), order.Address.String)
		} else {
			f.SetCellValue(sheet, fmt.Sprintf("G%d", row), "Не указано")
		}

		if order.DateRegister.Valid {
			f.SetCellValue(sheet, fmt.Sprintf("H%d", row), order.DateRegister.String)
		} else {
			f.SetCellValue(sheet, fmt.Sprintf("H%d", row), "")
		}

		f.SetCellValue(sheet, fmt.Sprintf("I%d", row), order.DatePay)

		// Checks status
		checksText := "❌ Не проверен"
		if order.Checks {
			checksText = "✅ Проверен"
		}
		f.SetCellValue(sheet, fmt.Sprintf("J%d", row), checksText)

		// Determine status and color
		var statusText, fillColor string
		if !order.Checks {
			statusText = "🔄 В обработке"
			fillColor = "#FEF3C7" // Yellow - pending
		} else if !order.Fio.Valid || order.Fio.String == "" {
			statusText = "⚠️ Неполные данные"
			fillColor = "#FEE2E2" // Red - incomplete
		} else if !order.Address.Valid || order.Address.String == "" {
			statusText = "📍 Нет адреса"
			fillColor = "#FECACA" // Light red - no address
		} else {
			statusText = "✅ Готов к доставке"
			fillColor = "#D1FAE5" // Green - ready
		}

		f.SetCellValue(sheet, fmt.Sprintf("K%d", row), statusText)

		// Apply row styling
		rowStyle, _ := f.NewStyle(&excelize.Style{
			Fill: excelize.Fill{Type: "pattern", Color: []string{fillColor}, Pattern: 1},
			Border: []excelize.Border{
				{Type: "left", Color: "#E5E7EB", Style: 1},
				{Type: "top", Color: "#E5E7EB", Style: 1},
				{Type: "bottom", Color: "#E5E7EB", Style: 1},
				{Type: "right", Color: "#E5E7EB", Style: 1},
			},
		})
		f.SetCellStyle(sheet,
			fmt.Sprintf("A%d", row),
			fmt.Sprintf("K%d", row),
			rowStyle,
		)
	}

	// 7. Auto-fit columns
	columnWidths := []float64{8, 12, 15, 10, 20, 15, 25, 15, 15, 15, 20}
	for i, width := range columnWidths {
		col := string('A' + i)
		f.SetColWidth(sheet, col, col, width)
	}

	// 8. Add summary at the bottom
	summaryRow := len(orders) + 3
	f.SetCellValue(sheet, fmt.Sprintf("A%d", summaryRow), "СТАТИСТИКА:")

	// Count by status
	var pending, incomplete, noAddress, ready int
	for _, order := range orders {
		if !order.Checks {
			pending++
		} else if !order.Fio.Valid || order.Fio.String == "" {
			incomplete++
		} else if !order.Address.Valid || order.Address.String == "" {
			noAddress++
		} else {
			ready++
		}
	}

	f.SetCellValue(sheet, fmt.Sprintf("A%d", summaryRow+1), fmt.Sprintf("🔄 В обработке: %d", pending))
	f.SetCellValue(sheet, fmt.Sprintf("A%d", summaryRow+2), fmt.Sprintf("⚠️ Неполные данные: %d", incomplete))
	f.SetCellValue(sheet, fmt.Sprintf("A%d", summaryRow+3), fmt.Sprintf("📍 Нет адреса: %d", noAddress))
	f.SetCellValue(sheet, fmt.Sprintf("A%d", summaryRow+4), fmt.Sprintf("✅ Готов к доставке: %d", ready))
	f.SetCellValue(sheet, fmt.Sprintf("A%d", summaryRow+5), fmt.Sprintf("📦 ВСЕГО ЗАКАЗОВ: %d", len(orders)))

	// Style summary
	summaryStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "#1F2937"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#F3F4F6"}, Pattern: 1},
	})
	f.SetCellStyle(sheet,
		fmt.Sprintf("A%d", summaryRow),
		fmt.Sprintf("A%d", summaryRow+5),
		summaryStyle,
	)

	// 9. Save file
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("orders_%s.xlsx", timestamp)
	filepath := filepath.Join(excelDir, filename)

	if err := f.SaveAs(filepath); err != nil {
		h.logger.Error("save excel failed", zap.Error(err))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.From.ID,
			Text:   "❌ Қате: Excel файлын сақтау мүмкін болмады",
		})
		return
	}

	// 10. Send summary message
	summaryMsg := fmt.Sprintf(
		"📦 Тапсырыстар экспортталды!\n\n"+
			"📊 Статистика:\n"+
			"🔄 В обработке: %d\n"+
			"⚠️ Неполные данные: %d\n"+
			"📍 Нет адреса: %d\n"+
			"✅ Готов к доставке: %d\n\n"+
			"📁 Файл: %s\n"+
			"📅 Дата: %s",
		pending, incomplete, noAddress, ready,
		filename,
		time.Now().Format("2006-01-02 15:04:05"),
	)

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.From.ID,
		Text:   summaryMsg,
	})

	// 11. Send Excel file
	h.sendExcelFile(ctx, b, update, filepath, "📦 Экспорт заказов Meily Cosmetics")
}

func (h *Handler) handleOrders(ctx context.Context, b *bot.Bot, update *models.Update) {
	// 1. Fetch everything
	entries, err := h.repo.GetAllLotoEntries(ctx)
	if err != nil {
		h.logger.Error("failed to load loto entries", zap.Error(err))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.From.ID,
			Text:   "❌ Қате: Лото деректерін алу мүмкін болмады",
		})
		return
	}

	// 2. Group by UserID
	byUser := make(map[int64][]domain.LotoEntry)
	for _, e := range entries {
		byUser[e.UserID] = append(byUser[e.UserID], e)
	}

	// 3. Prepare Excel
	excelDir := "./excel"
	if err := os.MkdirAll(excelDir, 0755); err != nil {
		h.logger.Error("mkdir excel failed", zap.Error(err))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.From.ID,
			Text:   "❌ Қате: Excel қалтасын жасау мүмкін болмады",
		})
		return
	}

	f := excelize.NewFile()
	defer f.Close()
	sheet := "Sheet1"
	f.SetSheetName(f.GetSheetName(f.GetActiveSheetIndex()), sheet)

	// 4. Write headers (with ID, DateRegister, DateUpdated)
	headers := []string{
		"ID",
		"UserID",
		"Тапсырыс саны",
		"Аты-жөні",
		"Contact",
		"Address",
		"DatePay",
		"DateUpdated",
	}
	for i, hcell := range headers {
		cell := fmt.Sprintf("%c1", 'A'+i)
		f.SetCellValue(sheet, cell, hcell)
	}
	// Bold header row
	hdrStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#4CAF50"}, Pattern: 1},
	})
	f.SetCellStyle(sheet, "A1", fmt.Sprintf("%c1", 'A'+len(headers)-1), hdrStyle)

	// 5. Fill grouped data + conditional row coloring
	row := 2
	counter := 1
	for userID, group := range byUser {
		// auto-increment ID
		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), counter)

		// count orders
		cnt := len(group) / 3
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), userID)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), cnt)

		// first entry for contact/address & dates
		first := group[0]
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), first.Fio.String)
		f.SetCellValue(sheet, fmt.Sprintf("E%d", row), first.Contact.String)
		f.SetCellValue(sheet, fmt.Sprintf("F%d", row), first.Address.String)
		f.SetCellValue(sheet, fmt.Sprintf("G%d", row), first.DatePay)
		f.SetCellValue(sheet, fmt.Sprintf("H%d", row), first.UpdatedAt)

		// decide row style
		var fillColor string
		if first.Contact.String == "" {
			fillColor = "#FEE2E2" // red
		} else if first.Address.String == "" {
			fillColor = "#FEF3C7" // yellow
		} else {
			fillColor = "#D1FAE5" // green
		}
		style, _ := f.NewStyle(&excelize.Style{
			Fill: excelize.Fill{Type: "pattern", Color: []string{fillColor}, Pattern: 1},
		})
		f.SetCellStyle(sheet,
			fmt.Sprintf("A%d", row),
			fmt.Sprintf("G%d", row),
			style,
		)

		row++
		counter++
	}

	// 6. Auto-fit columns
	for i := 0; i < len(headers); i++ {
		col := string('A' + i)
		f.SetColWidth(sheet, col, col, 18)
	}

	// 7. Save & send
	ts := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("orders_%s.xlsx", ts)
	path := filepath.Join(excelDir, filename)
	if err := f.SaveAs(path); err != nil {
		h.logger.Error("save excel failed", zap.Error(err))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.From.ID,
			Text:   "❌ Қате: Excel файлын сақтау мүмкін болмады",
		})
		return
	}

	// summary
	msg := fmt.Sprintf("📦 %d пайдаланушыдан %d жол экспортталды\n\n📁 Файл: %s",
		len(byUser), len(byUser), filename,
	)
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.From.ID,
		Text:   msg,
	})

	// send document
	h.sendExcelFile(ctx, b, update, path, "📦 Қолданушылар тапсырыстары")
}

func (h *Handler) SendMessage(ctx context.Context, b *bot.Bot, update *models.Update) {

	var adminId int64
	switch update.Message.From.ID {
	case h.cfg.AdminID:
		adminId = h.cfg.AdminID
	case h.cfg.AdminID2:
		adminId = h.cfg.AdminID2
	case h.cfg.AdminID3:
		adminId = h.cfg.AdminID3
	default:
		h.logger.Warn("SomeOne is trying to get admin root", zap.Any("user_id", update.Message.From.ID))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   fmt.Sprintf("SomeOne is trying to get admin root, user_id: %d", update.Message.From.ID),
		})
	}

	adminState, errRedis := h.redisRepo.GetUserState(ctx, adminId)
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
		if err := h.redisRepo.DeleteUserState(ctx, adminId); err != nil {
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
		userIds, err = h.repo.GetAllJustUserIDs(ctx)
	case "clients":
		// Assuming you have this method in repository
		userIds, err = h.repo.GetAllJustUserIDs(ctx) // For now, using same as all
	case "loto":
		userIds, err = h.repo.GetAllJustUserIDs(ctx) // For now, using same as all
	case "just":
		userIds, err = h.repo.GetAllJustUserIDs(ctx)
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

	if err := h.redisRepo.DeleteUserState(ctx, adminId); err != nil {
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
	case h.cfg.AdminID2:
		adminId = h.cfg.AdminID2
	case h.cfg.AdminID3:
		adminId = h.cfg.AdminID3
	default:
		h.logger.Warn("SomeOne is trying to get admin root", zap.Any("user_id", update.Message.From.ID))
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   fmt.Sprintf("SomeOne is trying to get admin root, user_id: %d", update.Message.From.ID),
		})
	}

	// Get counts for each category
	allCount, _ := h.repo.GetAllJustUserIDs(ctx)

	broadcastState := &domain.UserState{
		State: stateBroadcast,
	}
	if err := h.redisRepo.SaveUserState(ctx, adminId, broadcastState); err != nil {
		h.logger.Error("Failed to save broadcast state to Redis", zap.Error(err))
	}

	broadcastKeyboard := &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{
				{Text: "📢 Барлығына жіберу"},
				{Text: "🛍 Клиенттерге жіберу"},
			},
			{
				{Text: "🎲 Лото қатысушыларына "},
				{Text: "👥 Тіркелгендерге"},
			},
			{
				{Text: "🔙 Артқа (Back)"},
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
	case h.cfg.AdminID2:
		adminId = h.cfg.AdminID2
	case h.cfg.AdminID3:
		adminId = h.cfg.AdminID3
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
	if err := h.redisRepo.SaveUserState(ctx, adminId, broadCastState); err != nil {
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

func (h *Handler) handleMoneyStats(ctx context.Context, b *bot.Bot) {
	// Get total money
	totalMoney, err := h.repo.GetMoneyStats(ctx)
	if err != nil {
		h.logger.Error("Failed to get money stats", zap.Error(err))
		totalMoney = 0
	}

	// Get today's earnings
	todayEarnings, err := h.repo.GetTodayEarnings(ctx)
	if err != nil {
		h.logger.Error("Failed to get today earnings", zap.Error(err))
		todayEarnings = 0
	}

	// Get payment count
	paymentCount, err := h.repo.GetPaymentCount(ctx)
	if err != nil {
		h.logger.Error("Failed to get payment count", zap.Error(err))
		paymentCount = 0
	}

	// Format the message
	statsMessage := fmt.Sprintf(
		"💰 АҚША СТАТИСТИКАСЫ\n\n"+
			"💵 Жалпы сумма: %s ₸\n"+
			"📅 Бүгінгі табыс: %s ₸\n"+
			"🧾 Жалпы төлемдер: %d\n"+
			"⏰ Соңғы жаңарту: %s",
		formatMoney(totalMoney),
		formatMoney(todayEarnings),
		paymentCount,
		time.Now().Format("15:04:05"),
	)

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   statsMessage,
	})
	if err != nil {
		h.logger.Error("Failed to send money stats", zap.Error(err))
	}
}

// Helper function to format money with thousands separator
func formatMoney(amount int) string {
	str := strconv.Itoa(amount)
	n := len(str)
	if n <= 3 {
		return str
	}

	result := ""
	for i, digit := range str {
		if i > 0 && (n-i)%3 == 0 {
			result += " "
		}
		result += string(digit)
	}
	return result
}

// handleJustUsers exports all users from the 'just' table to Excel
func (h *Handler) handleJustUsers(ctx context.Context, b *bot.Bot, update *models.Update) {
	// Get all user IDs from just table
	userIds, err := h.repo.GetAllJustUserIDs(ctx)
	if err != nil {
		h.logger.Error("Failed to get just users", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Пайдаланушылар деректерін алу мүмкін болмады",
		})
		return
	}

	// Get detailed entries
	justEntries, err := h.repo.GetRecentJustEntries(ctx, len(userIds))
	if err != nil {
		h.logger.Error("Failed to get just entries", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Толық деректерді алу мүмкін болмады",
		})
		return
	}

	// Create Excel file
	excelDir := "./excel"
	err = os.MkdirAll(excelDir, 0755)
	if err != nil {
		h.logger.Error("Failed to create excel directory", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Excel қалтасын жасау мүмкін болмады",
		})
		return
	}

	// Generate Excel file
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("just_users_%s.xlsx", timestamp)
	filePath := filepath.Join(excelDir, filename)

	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			h.logger.Error("Failed to close Excel file", zap.Error(err))
		}
	}()

	// Set headers
	headers := []string{"ID", "Пайдаланушы ID", "Аты", "Тіркелген күні", "Жалпы саны"}
	for i, header := range headers {
		cell := fmt.Sprintf("%c1", 'A'+i)
		f.SetCellValue("Sheet1", cell, header)
	}

	// Style headers
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 12, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#4472C4"}, Pattern: 1},
	})
	f.SetCellStyle("Sheet1", "A1", fmt.Sprintf("%c1", 'A'+len(headers)-1), headerStyle)

	// Add data
	for i, entry := range justEntries {
		row := i + 2
		f.SetCellValue("Sheet1", fmt.Sprintf("A%d", row), i+1)
		f.SetCellValue("Sheet1", fmt.Sprintf("B%d", row), entry.UserID)
		f.SetCellValue("Sheet1", fmt.Sprintf("C%d", row), entry.UserName)
		f.SetCellValue("Sheet1", fmt.Sprintf("D%d", row), entry.DateRegistered)
		if i == 0 {
			f.SetCellValue("Sheet1", fmt.Sprintf("E%d", row), len(userIds))
		}
	}

	// Auto-fit columns
	for i := 0; i < len(headers); i++ {
		col := string(rune('A' + i))
		f.SetColWidth("Sheet1", col, col, 15)
	}

	// Save file
	if err := f.SaveAs(filePath); err != nil {
		h.logger.Error("Failed to save Excel file", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Excel файлын сақтау мүмкін болмады",
		})
		return
	}

	// Send summary message
	message := fmt.Sprintf("👥 ТІРКЕЛГЕН ПАЙДАЛАНУШЫЛАР\n\nЖалпы: %d пайдаланушы\n📊 Excel файл дайындалды", len(userIds))
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   message,
	})
	if err != nil {
		h.logger.Error("Failed to send just users message", zap.Error(err))
	}

	// Send Excel file
	h.sendExcelFile(ctx, b, update, filePath, "👥 Тіркелген пайдаланушылар тізімі")
}

// handleClients exports all clients from the 'client' table to Excel
func (h *Handler) handleClients(ctx context.Context, b *bot.Bot, update *models.Update) {
	// Get all clients with geo data
	clientEntries, err := h.repo.GetClientsWithGeo(ctx)
	if err != nil {
		h.logger.Error("Failed to get client entries", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Клиенттер деректерін алу мүмкін болмады",
		})
		return
	}

	// Create Excel directory
	excelDir := "./excel"
	err = os.MkdirAll(excelDir, 0755)
	if err != nil {
		h.logger.Error("Failed to create excel directory", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Excel қалтасын жасау мүмкін болмады",
		})
		return
	}

	// Generate Excel file
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("clients_%s.xlsx", timestamp)
	filePath := filepath.Join(excelDir, filename)

	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			h.logger.Error("Failed to close Excel file", zap.Error(err))
		}
	}()

	// Set headers
	headers := []string{
		"ID", "Пайдаланушы ID", "Аты", "ФИО", "Байланыс",
		"Мекенжай", "Тіркелген күні", "Төлем күні", "Тексерілді",
		"Геолокация", "Кеңдік", "Ұзындық", "Дәлдік (м)", "Қала", "Ел",
	}

	for i, header := range headers {
		cell := fmt.Sprintf("%c1", 'A'+i)
		f.SetCellValue("Sheet1", cell, header)
	}

	// Style headers
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 11, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#10B981"}, Pattern: 1},
	})
	f.SetCellStyle("Sheet1", "A1", fmt.Sprintf("%c1", 'A'+len(headers)-1), headerStyle)

	// Add data
	deliveredCount := 0
	geoCount := 0

	for i, entry := range clientEntries {
		row := i + 2
		f.SetCellValue("Sheet1", fmt.Sprintf("A%d", row), i+1)
		f.SetCellValue("Sheet1", fmt.Sprintf("B%d", row), entry.UserID)
		f.SetCellValue("Sheet1", fmt.Sprintf("C%d", row), entry.UserName)
		f.SetCellValue("Sheet1", fmt.Sprintf("D%d", row), entry.Fio)
		f.SetCellValue("Sheet1", fmt.Sprintf("E%d", row), entry.Contact)
		f.SetCellValue("Sheet1", fmt.Sprintf("F%d", row), entry.Address)
		f.SetCellValue("Sheet1", fmt.Sprintf("G%d", row), entry.DateRegister)
		f.SetCellValue("Sheet1", fmt.Sprintf("H%d", row), entry.DatePay)

		// Delivery status
		deliveryStatus := "Жоқ"
		if entry.Checks {
			deliveryStatus = "Ия"
			deliveredCount++
		}
		f.SetCellValue("Sheet1", fmt.Sprintf("I%d", row), deliveryStatus)

		// Geo data
		geoStatus := "Жоқ"
		if entry.HasGeo {
			geoStatus = "Ия"
			geoCount++
			if entry.Latitude != nil {
				f.SetCellValue("Sheet1", fmt.Sprintf("K%d", row), *entry.Latitude)
			}
			if entry.Longitude != nil {
				f.SetCellValue("Sheet1", fmt.Sprintf("L%d", row), *entry.Longitude)
			}
			if entry.AccuracyMeters != nil {
				f.SetCellValue("Sheet1", fmt.Sprintf("M%d", row), *entry.AccuracyMeters)
			}
			if entry.City != nil {
				f.SetCellValue("Sheet1", fmt.Sprintf("N%d", row), *entry.City)
			}
			f.SetCellValue("Sheet1", fmt.Sprintf("O%d", row), entry.Country)
		}
		f.SetCellValue("Sheet1", fmt.Sprintf("J%d", row), geoStatus)
	}

	// Auto-fit columns
	columnWidths := []float64{5, 12, 15, 20, 15, 25, 18, 18, 10, 12, 12, 12, 10, 15, 12}
	for i, width := range columnWidths {
		col := string(rune('A' + i))
		f.SetColWidth("Sheet1", col, col, width)
	}

	// Save file
	if err := f.SaveAs(filePath); err != nil {
		h.logger.Error("Failed to save Excel file", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Excel файлын сақтау мүмкін болмады",
		})
		return
	}

	// Send summary message
	message := fmt.Sprintf("🛍 КЛИЕНТТЕР\n\n"+
		"Жалпы клиенттер: %d\n"+
		"Жеткізілген: %d\n"+
		"Геолокациясы бар: %d\n"+
		"📊 Excel файл дайындалды",
		len(clientEntries), deliveredCount, geoCount)

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   message,
	})
	if err != nil {
		h.logger.Error("Failed to send clients message", zap.Error(err))
	}

	// Send Excel file
	h.sendExcelFile(ctx, b, update, filePath, "🛍 Клиенттер тізімі")
}

// handleLoto exports all loto entries from the 'loto' table to Excel
func (h *Handler) handleLoto(ctx context.Context, b *bot.Bot, update *models.Update) {
	// Get all loto entries
	lotoEntries, err := h.repo.GetRecentLotoEntries(ctx, 10000) // Get a large number to get all
	if err != nil {
		h.logger.Error("Failed to get loto entries", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Лото деректерін алу мүмкін болмады",
		})
		return
	}

	// Create Excel directory
	excelDir := "./excel"
	err = os.MkdirAll(excelDir, 0755)
	if err != nil {
		h.logger.Error("Failed to create excel directory", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Excel қалтасын жасау мүмкін болмады",
		})
		return
	}

	// Generate Excel file
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("loto_%s.xlsx", timestamp)
	filePath := filepath.Join(excelDir, filename)

	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			h.logger.Error("Failed to close Excel file", zap.Error(err))
		}
	}()

	// Set headers
	headers := []string{
		"ID", "Пайдаланушы ID", "Лото ID", "QR Код", "Төлеуші",
		"Чек", "ФИО", "Байланыс", "Мекенжай", "Төлем күні", "Статус",
	}

	for i, header := range headers {
		cell := fmt.Sprintf("%c1", 'A'+i)
		f.SetCellValue("Sheet1", cell, header)
	}

	// Style headers
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 11, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#F59E0B"}, Pattern: 1},
	})
	f.SetCellStyle("Sheet1", "A1", fmt.Sprintf("%c1", 'A'+len(headers)-1), headerStyle)

	// Add data and count statistics
	paidCount := 0
	unpaidCount := 0

	for i, entry := range lotoEntries {
		row := i + 2
		f.SetCellValue("Sheet1", fmt.Sprintf("A%d", row), i+1)
		f.SetCellValue("Sheet1", fmt.Sprintf("B%d", row), entry.UserID)
		f.SetCellValue("Sheet1", fmt.Sprintf("C%d", row), entry.LotoID)
		f.SetCellValue("Sheet1", fmt.Sprintf("D%d", row), entry.QR)
		f.SetCellValue("Sheet1", fmt.Sprintf("E%d", row), entry.WhoPaid)
		f.SetCellValue("Sheet1", fmt.Sprintf("F%d", row), entry.Receipt)
		f.SetCellValue("Sheet1", fmt.Sprintf("G%d", row), entry.Fio)
		f.SetCellValue("Sheet1", fmt.Sprintf("H%d", row), entry.Contact)
		f.SetCellValue("Sheet1", fmt.Sprintf("I%d", row), entry.Address)
		f.SetCellValue("Sheet1", fmt.Sprintf("J%d", row), entry.DatePay)

		// Payment status
		status := "Төленбеген"
		if entry.WhoPaid.String != "" {
			status = "Төленген"
			paidCount++
		} else {
			unpaidCount++
		}
		f.SetCellValue("Sheet1", fmt.Sprintf("K%d", row), status)

		// Color code based on payment status
		if entry.WhoPaid.String != "" {
			// Green for paid
			paidStyle, _ := f.NewStyle(&excelize.Style{
				Fill: excelize.Fill{Type: "pattern", Color: []string{"#D1FAE5"}, Pattern: 1},
			})
			f.SetCellStyle("Sheet1", fmt.Sprintf("A%d", row), fmt.Sprintf("K%d", row), paidStyle)
		} else {
			// Light red for unpaid
			unpaidStyle, _ := f.NewStyle(&excelize.Style{
				Fill: excelize.Fill{Type: "pattern", Color: []string{"#FEE2E2"}, Pattern: 1},
			})
			f.SetCellStyle("Sheet1", fmt.Sprintf("A%d", row), fmt.Sprintf("K%d", row), unpaidStyle)
		}
	}

	// Auto-fit columns
	columnWidths := []float64{5, 12, 8, 15, 15, 15, 20, 15, 25, 18, 12}
	for i, width := range columnWidths {
		col := string(rune('A' + i))
		f.SetColWidth("Sheet1", col, col, width)
	}

	// Add summary sheet
	f.NewSheet("Статистика")
	f.SetCellValue("Статистика", "A1", "ЛОТО СТАТИСТИКАСЫ")
	f.SetCellValue("Статистика", "A3", "Жалпы қатысушылар:")
	f.SetCellValue("Статистика", "B3", len(lotoEntries))
	f.SetCellValue("Статистика", "A4", "Төленген:")
	f.SetCellValue("Статистика", "B4", paidCount)
	f.SetCellValue("Статистика", "A5", "Төленбеген:")
	f.SetCellValue("Статистика", "B5", unpaidCount)
	f.SetCellValue("Статистика", "A6", "Төлем пайызы:")

	paymentPercentage := 0.0
	if len(lotoEntries) > 0 {
		paymentPercentage = float64(paidCount) / float64(len(lotoEntries)) * 100
	}
	f.SetCellValue("Статистика", "B6", fmt.Sprintf("%.1f%%", paymentPercentage))

	// Style summary
	summaryStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14, Color: "#FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#F59E0B"}, Pattern: 1},
	})
	f.SetCellStyle("Статистика", "A1", "A1", summaryStyle)

	// Save file
	if err := f.SaveAs(filePath); err != nil {
		h.logger.Error("Failed to save Excel file", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Excel файлын сақтау мүмкін болмады",
		})
		return
	}

	// Send summary message
	message := fmt.Sprintf("🎲 ЛОТО\n\n"+
		"Жалпы қатысушылар: %d\n"+
		"Төленген: %d\n"+
		"Төленбеген: %d\n"+
		"Төлем пайызы: %.1f%%\n"+
		"📊 Excel файл дайындалды",
		len(lotoEntries), paidCount, unpaidCount, paymentPercentage)

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   message,
	})
	if err != nil {
		h.logger.Error("Failed to send loto message", zap.Error(err))
	}

	// Send Excel file
	//h.sendExcelFile(ctx, b, update, filePath, "🎲 Лото қатысушылар тізімі")
}

// sendExcelFile sends the Excel file to admin via Telegram
func (h *Handler) sendExcelFile(ctx context.Context, b *bot.Bot, update *models.Update, filePath, caption string) {
	var adminId int64
	if update.Message.From.ID == h.cfg.AdminID2 {
		adminId = h.cfg.AdminID2
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
		Caption:  caption + "\n\n📁 Файл: " + filepath.Base(filePath) + "\n📊 Өлшемі: " + formatFileSize(fileInfo.Size()),
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

// handleGift - 5-step random selection: 10 → 7 → 4 → 3 → 1 winner
func (h *Handler) handleGift(ctx context.Context, b *bot.Bot) {
	// Get all loto entries
	allLotoEntries, err := h.repo.GetAllLotoEntries(ctx)
	if err != nil {
		h.logger.Error("Failed to get loto entries", zap.Error(err))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   "❌ Қате: Лото деректерін алу мүмкін болмады",
		})
		return
	}

	// Filter entries with valid contact only
	var validEntries []domain.LotoEntry
	for _, entry := range allLotoEntries {
		if entry.Contact.Valid && entry.Contact.String != "" {
			validEntries = append(validEntries, entry)
		}
	}

	// Check if we have enough participants with contacts
	if len(validEntries) < 10 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID2,
			Text:   fmt.Sprintf("🎁 СЫЙЛЫҚ\n\n⚠️ Байланыс нөмірі бар кем дегенде 10 қатысушы қажет. Қазіргі: %d", len(validEntries)),
		})
		return
	}

	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

	// Initial message
	initialMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID2,
		Text:   "🎁 СЫЙЛЫҚ ОЙЫНЫ БАСТАЛДЫ!\n\n🎲 Кездейсоқ таңдау жүріп жатыр...",
	})
	if err != nil {
		h.logger.Error("Failed to send initial gift message", zap.Error(err))
		return
	}
	messageID := int(initialMsg.ID)

	// Step 1: Select 10 random participants
	step1 := getRandomLotoEntries(validEntries, 10)
	h.updateGiftStep(ctx, b, messageID, "🎁 1-КЕЗЕҢ - 10 ҚАТЫСУШЫ", len(validEntries), step1)
	time.Sleep(3 * time.Second)

	// Step 2: 10 → 7
	step2 := getRandomLotoEntries(step1, 7)
	h.updateGiftStep(ctx, b, messageID, "🎁 2-КЕЗЕҢ - 7 ҚАТЫСУШЫ", len(validEntries), step2)
	time.Sleep(3 * time.Second)

	// Step 3: 7 → 4
	step3 := getRandomLotoEntries(step2, 4)
	h.updateGiftStep(ctx, b, messageID, "🎁 3-КЕЗЕҢ - 4 ҚАТЫСУШЫ", len(validEntries), step3)
	time.Sleep(3 * time.Second)

	// Step 4: 4 → 3
	step4 := getRandomLotoEntries(step3, 3)
	h.updateGiftStep(ctx, b, messageID, "🎁 4-КЕЗЕҢ - 3 ҚАТЫСУШЫ", len(validEntries), step4)
	time.Sleep(3 * time.Second)

	// Step 5: 3 → 1 (Final winner)
	finalWinner := getRandomLotoEntries(step4, 1)[0]

	// Extract winner info
	var fio, contact string
	if finalWinner.Fio.Valid {
		fio = finalWinner.Fio.String
	} else {
		fio = "Белгісіз"
	}

	if finalWinner.Contact.Valid {
		contact = finalWinner.Contact.String
	} else {
		contact = "Белгісіз"
	}

	// Build final winner message
	winnerMsg := fmt.Sprintf(
		"🎁 СЫЙЛЫҚ ОЙЫНЫ НӘТИЖЕСІ!\n\n"+
			"🎉 ҚҰТТЫҚТАЙМЫЗ!\n\n"+
			"👤 Жеңімпаз: %s\n"+
			"📱 Байланыс: %s\n"+
			"🎲 ID: %d\n\n"+
			"✅ Сыйлықты алу үшін администрациямен байланысыңыз!",
		fio,
		contact,
		finalWinner.LotoID,
	)

	// Send final winner announcement
	_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    h.cfg.AdminID2,
		MessageID: messageID,
		Text:      winnerMsg,
	})
	if err != nil {
		h.logger.Error("Failed to edit message with final winner", zap.Error(err))
	}
}

// updateGiftStep updates the message with current step participants
func (h *Handler) updateGiftStep(ctx context.Context, b *bot.Bot, messageID int, stepTitle string, totalParticipants int, participants []domain.LotoEntry) {
	var participantsList []string
	for i, p := range participants {
		var fio string
		if p.Fio.Valid && p.Fio.String != "" {
			fio = p.Fio.String
		} else {
			fio = fmt.Sprintf("User_%d", p.UserID)
		}
		participantsList = append(participantsList, fmt.Sprintf("%d. %s (ID: %d)", i+1, fio, p.UserID))
	}

	stepMsg := fmt.Sprintf(
		"%s\n\n"+
			"📊 Жалпы қатысушылар: %d\n"+
			"🎯 Қалған қатысушылар: %d\n\n"+
			"👥 ҚАТЫСУШЫЛАР:\n%s\n\n"+
			"⏳ Келесі кезеңге дайындалуда...",
		stepTitle,
		totalParticipants,
		len(participants),
		strings.Join(participantsList, "\n"),
	)

	_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    h.cfg.AdminID2,
		MessageID: messageID,
		Text:      stepMsg,
	})
	if err != nil {
		h.logger.Error("Failed to edit message", zap.Error(err))
	}
}

// formatFileSize formats file size in human readable format
func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// buildGiftMessage creates a message for intermediate steps with step counter
func (h *Handler) buildGiftMessage(title string, totalParticipants int, entries []domain.LotoEntry, currentStep, totalSteps int) string {
	message := fmt.Sprintf("%s\n\n", title)
	message += fmt.Sprintf("Таңдалған: %d\n", len(entries))
	message += fmt.Sprintf("Қадам: %d/%d\n\n", currentStep, totalSteps)

	for i, entry := range entries {
		// Handle sql.NullString fields safely
		fio := "Көрсетілмеген"
		if entry.Fio.Valid && entry.Fio.String != "" {
			fio = entry.Fio.String
		}

		// Format entry info (simplified for intermediate steps)
		message += fmt.Sprintf("🎲 %d. %s (ID: %d)\n", i+1, fio, entry.UserID)

		// Telegram message size limit check
		if len(message) > 3800 { // Leave room for footer
			message += fmt.Sprintf("\n... және тағы %d қатысушы\n", len(entries)-i-1)
			break
		}
	}

	if currentStep < totalSteps {
		message += "\n⏳ Келесі кезеңге дайындалуда..."
	}

	return message
}

// buildFinalGiftMessage creates the final message with detailed info for winners
func (h *Handler) buildFinalGiftMessage(totalParticipants int, winners []domain.LotoEntry) string {
	message := "🏆 СЫЙЛЫҚ ЖЕҢІМПАЗДАРЫ!\n\n"
	message += fmt.Sprintf("Жалпы қатысушылар: %d\n", totalParticipants)
	message += fmt.Sprintf("🎉 ЖЕҢІМПАЗДАР: %d\n\n", len(winners))

	for i, entry := range winners {
		// Handle sql.NullString fields safely
		fio := "Көрсетілмеген"
		if entry.Fio.Valid && entry.Fio.String != "" {
			fio = entry.Fio.String
		}

		contact := "Көрсетілмеген"
		if entry.Contact.Valid && entry.Contact.String != "" {
			contact = entry.Contact.String
		}

		// Format winner info with full details
		message += fmt.Sprintf("🏆 %d.\n", i+1)
		message += fmt.Sprintf("👤 ID: %d\n", entry.UserID)
		message += fmt.Sprintf("📝 ФИО: %s\n", fio)
		message += fmt.Sprintf("📞 Байланыс: %s\n", contact)
		message += "\n"

		// Check message size limit
		if len(message) > 3500 && i < len(winners)-1 {
			// If message is getting too long and there are more winners,
			// we might need to send multiple messages
			break
		}
	}

	message += "🎊 Құттықтаймыз!"
	return message
}

// getRandomLotoEntries selects n random entries from the slice
// This function should be implemented to randomly select entries
func getRandomLotoEntries(entries []domain.LotoEntry, count int) []domain.LotoEntry {
	if len(entries) <= count {
		return entries
	}

	// Create a copy of the slice to avoid modifying the original
	entriesCopy := make([]domain.LotoEntry, len(entries))
	copy(entriesCopy, entries)

	// Shuffle the copy using Fisher-Yates algorithm
	rand.Seed(time.Now().UnixNano())
	for i := len(entriesCopy) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		entriesCopy[i], entriesCopy[j] = entriesCopy[j], entriesCopy[i]
	}

	// Return the first 'count' entries
	return entriesCopy[:count]
}

func (h *Handler) handleStatistics(ctx context.Context, b *bot.Bot) {
	userIds, _ := h.repo.GetAllJustUserIDs(ctx)

	message := fmt.Sprintf(`📊 ЖАЛПЫ СТАТИСТИКА

👥 Жалпы пайдаланушылар: %d
🛍 Клиенттер: 0
🎲 Лото қатысушылары: 0

📅 Соңғы жаңарту: %s`,
		len(userIds),
		time.Now().Format("2006-01-02 15:04:05"))

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   message,
	})
	if err != nil {
		h.logger.Error("Failed to send statistics", zap.Error(err))
	}
}

func (h *Handler) handleCloseAdmin(ctx context.Context, b *bot.Bot) {
	if err := h.redisRepo.DeleteUserState(ctx, h.cfg.AdminID); err != nil {
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