//go:build darwin && cgo

package collect

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework CoreWLAN -framework Foundation
#import <CoreWLAN/CoreWLAN.h>
#include <string.h>

typedef struct {
	int present, power, rssi, noise, channel, band, width, phy, security;
	double rate;
	char ifname[32], ssid[128], country[8];
} snet_wifi;

static void copy_str(char *dst, size_t n, NSString *s) {
	if (s) strlcpy(dst, s.UTF8String, n);
}

static snet_wifi snet_read_wifi(void) {
	snet_wifi w;
	memset(&w, 0, sizeof w);
	@autoreleasepool {
		CWInterface *i = [[CWWiFiClient sharedWiFiClient] interface];
		if (!i) return w;
		w.present = 1;
		w.power = i.powerOn;
		copy_str(w.ifname, sizeof w.ifname, i.interfaceName);
		copy_str(w.ssid, sizeof w.ssid, i.ssid);
		copy_str(w.country, sizeof w.country, i.countryCode);
		CWChannel *c = i.wlanChannel;
		if (c) {
			w.channel = (int)c.channelNumber;
			w.band = (int)c.channelBand;
			w.width = (int)c.channelWidth;
			w.rssi = (int)i.rssiValue;
			w.noise = (int)i.noiseMeasurement;
			w.rate = i.transmitRate;
			w.phy = (int)i.activePHYMode;
			w.security = (int)i.security;
		}
	}
	return w;
}
typedef struct { int total, same, strongest, err; } snet_scan;

static snet_scan snet_scan_wifi(int channel) {
	snet_scan r = {0, 0, 0, 0};
	@autoreleasepool {
		CWInterface *i = [[CWWiFiClient sharedWiFiClient] interface];
		if (!i) { r.err = 1; return r; }
		NSError *e = nil;
		NSSet<CWNetwork *> *nets = [i scanForNetworksWithName:nil error:&e];
		if (e || !nets) { r.err = 2; return r; }
		r.strongest = -200;
		for (CWNetwork *n in nets) {
			r.total++;
			if (n.wlanChannel && (int)n.wlanChannel.channelNumber == channel) {
				r.same++;
			}
			if ((int)n.rssiValue > r.strongest) r.strongest = (int)n.rssiValue;
		}
		if (r.strongest == -200) r.strongest = 0;
	}
	return r;
}
*/
import "C"

import "github.com/seanmizen/snetwork/internal/netinfo"

// ReadWiFi reads the Wi-Fi interface through CoreWLAN. It takes a few
// milliseconds, where system_profiler takes seconds. macOS hides the network
// name unless the terminal has Location Services permission; every other
// field is readable without it.
func ReadWiFi() (*netinfo.WiFiRaw, error) {
	w := C.snet_read_wifi()
	if w.present == 0 {
		return nil, nil
	}
	return &netinfo.WiFiRaw{
		Interface: C.GoString(&w.ifname[0]),
		PowerOn:   w.power != 0,
		SSID:      C.GoString(&w.ssid[0]),
		RSSI:      int(w.rssi),
		Noise:     int(w.noise),
		Rate:      float64(w.rate),
		Channel:   int(w.channel),
		Band:      int(w.band),
		Width:     int(w.width),
		PHY:       int(w.phy),
		Security:  int(w.security),
		Country:   C.GoString(&w.country[0]),
	}, nil
}

// ScanWiFi counts the networks in range, and those on channel. A scan
// takes a few seconds and changes nothing: macOS scans like this on its own.
func ScanWiFi(channel int) netinfo.WiFiScan {
	r := C.snet_scan_wifi(C.int(channel))
	if r.err != 0 {
		return netinfo.WiFiScan{Err: "scan failed", Done: true}
	}
	return netinfo.WiFiScan{Networks: int(r.total), SameChannel: int(r.same), Strongest: int(r.strongest), Done: true}
}
