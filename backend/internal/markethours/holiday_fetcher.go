package markethours

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HolidayEntry represents a single NSE trading holiday.
type HolidayEntry struct {
	Date string `json:"date"` // "2026-01-26"
	Name string `json:"name"` // "Republic Day"
}

// HolidayCache is the JSON structure persisted to config/holidays.json.
type HolidayCache struct {
	Year       int            `json:"year"`
	FetchedAt  string         `json:"fetched_at,omitempty"`
	Holidays   []HolidayEntry `json:"holidays"`
}

// nseHolidayResponse matches the NSE API JSON shape.
type nseHolidayResponse struct {
	CM []struct {
		TradingDate  string `json:"tradingDate"`  // "26-Mar-2026"
		Description  string `json:"description"`  // "Ram Navami"
		WeekDay      string `json:"weekDay"`
		Sr_no        int    `json:"sr_no"`
	} `json:"CM"`
}

const (
	nseBaseURL    = "https://www.nseindia.com"
	nseHolidayURL = "https://www.nseindia.com/api/holiday-master?type=trading"
	cacheFileName = "holidays.json"
	userAgent     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// FetchNSEHolidays fetches trading holidays from the NSE website API.
// NSE requires a valid session cookie, so we first hit the homepage.
func FetchNSEHolidays() ([]HolidayEntry, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil
		},
	}

	// Step 1: Hit homepage to get cookies
	homeReq, _ := http.NewRequest("GET", nseBaseURL, nil)
	homeReq.Header.Set("User-Agent", userAgent)
	homeReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	homeReq.Header.Set("Accept-Language", "en-US,en;q=0.5")

	homeResp, err := client.Do(homeReq)
	if err != nil {
		return nil, fmt.Errorf("NSE homepage fetch failed: %w", err)
	}
	io.ReadAll(homeResp.Body)
	homeResp.Body.Close()

	// Extract cookies
	cookies := homeResp.Cookies()

	// Step 2: Fetch holiday API with cookies
	apiReq, _ := http.NewRequest("GET", nseHolidayURL, nil)
	apiReq.Header.Set("User-Agent", userAgent)
	apiReq.Header.Set("Accept", "application/json")
	apiReq.Header.Set("Referer", nseBaseURL+"/regulations/holiday-master")
	for _, c := range cookies {
		apiReq.AddCookie(c)
	}

	apiResp, err := client.Do(apiReq)
	if err != nil {
		return nil, fmt.Errorf("NSE holiday API fetch failed: %w", err)
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != 200 {
		return nil, fmt.Errorf("NSE holiday API returned status %d", apiResp.StatusCode)
	}

	body, err := io.ReadAll(apiResp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading NSE response: %w", err)
	}

	var nseResp nseHolidayResponse
	if err := json.Unmarshal(body, &nseResp); err != nil {
		return nil, fmt.Errorf("parsing NSE response: %w", err)
	}

	// Parse CM (equity segment) holidays
	var holidays []HolidayEntry
	for _, h := range nseResp.CM {
		// Parse "26-Mar-2026" format
		t, err := time.Parse("02-Jan-2006", h.TradingDate)
		if err != nil {
			log.Printf("[markethours] skipping unparseable NSE date %q: %v", h.TradingDate, err)
			continue
		}
		holidays = append(holidays, HolidayEntry{
			Date: t.Format("2006-01-02"),
			Name: strings.TrimSpace(h.Description),
		})
	}

	if len(holidays) == 0 {
		return nil, fmt.Errorf("no CM holidays found in NSE response")
	}

	return holidays, nil
}

// SaveHolidayCache writes the holiday list to config/holidays.json.
func SaveHolidayCache(configDir string, holidays []HolidayEntry) error {
	year := time.Now().In(IST).Year()
	if len(holidays) > 0 {
		if t, err := time.Parse("2006-01-02", holidays[0].Date); err == nil {
			year = t.Year()
		}
	}

	cache := HolidayCache{
		Year:      year,
		FetchedAt: time.Now().In(IST).Format(time.RFC3339),
		Holidays:  holidays,
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(configDir, cacheFileName)
	return os.WriteFile(path, data, 0644)
}

// LoadHolidayCache reads the cached holiday list from config/holidays.json.
func LoadHolidayCache(configDir string) (HolidayCache, error) {
	var cache HolidayCache
	path := filepath.Join(configDir, cacheFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return cache, err
	}

	if err := json.Unmarshal(data, &cache); err != nil {
		return cache, err
	}

	return cache, nil
}
