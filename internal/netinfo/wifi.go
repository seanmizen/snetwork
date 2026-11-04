package netinfo

import "fmt"

// WiFiRaw is CoreWLAN's view of the Wi-Fi interface, as numbers. Package
// collect fills it; WiFiFromRaw names the enums. It has JSON tags so
// tests can load it from a fixture file.
type WiFiRaw struct {
	Interface string  `json:"interface"`
	PowerOn   bool    `json:"power_on"`
	SSID      string  `json:"ssid"`
	RSSI      int     `json:"rssi"`
	Noise     int     `json:"noise"`
	Rate      float64 `json:"rate"`
	Channel   int     `json:"channel"` // 0 when not associated
	Band      int     `json:"band"`    // CWChannelBand
	Width     int     `json:"width"`   // CWChannelWidth
	PHY       int     `json:"phy"`     // CWPHYMode
	Security  int     `json:"security"`
	Country   string  `json:"country"`
}

// WiFiFromRaw names CoreWLAN's enums.
func WiFiFromRaw(r WiFiRaw) *WiFi {
	w := &WiFi{Interface: r.Interface, PowerOn: r.PowerOn, Name: r.SSID, Country: r.Country}
	if !r.PowerOn || r.Channel == 0 {
		return w
	}
	w.Associated = true
	w.Channel = r.Channel
	w.RSSI, w.Noise, w.RateMbps = r.RSSI, r.Noise, r.Rate
	w.Band = pick(r.Band, map[int]string{1: "2.4 GHz", 2: "5 GHz", 3: "6 GHz"})
	w.Width = pick(r.Width, map[int]string{1: "20 MHz", 2: "40 MHz", 3: "80 MHz", 4: "160 MHz"})
	w.PHY = pick(r.PHY, map[int]string{1: "802.11a", 2: "802.11b", 3: "802.11g", 4: "802.11n", 5: "802.11ac", 6: "802.11ax", 7: "802.11be"})
	w.Security = pick(r.Security, map[int]string{
		0: "open", 1: "WEP", 2: "WPA Personal", 3: "WPA/WPA2 Personal", 4: "WPA2 Personal",
		5: "Personal", 6: "dynamic WEP", 7: "WPA Enterprise", 8: "WPA/WPA2 Enterprise",
		9: "WPA2 Enterprise", 10: "Enterprise", 11: "WPA3 Personal", 12: "WPA3 Enterprise",
		13: "WPA2/WPA3 Personal", 14: "OWE", 15: "OWE transition",
	})
	return w
}

func pick(n int, names map[int]string) string {
	if s, ok := names[n]; ok {
		return s
	}
	return fmt.Sprintf("unknown (%d)", n)
}
