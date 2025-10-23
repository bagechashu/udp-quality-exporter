package main

import (
	"testing"
)

func TestIP2Country(t *testing.T) {
	country := GetCountryByIPLocal("8.8.4.4", "GeoLite2-Country.mmdb")
	t.Logf("Country: %s", country)
	bogon := GetCountryByIPLocal("192.168.56.100", "GeoLite2-Country.mmdb")
	t.Logf("Country: %s", bogon)
	noGeoDB := GetCountryByIPLocal("192.168.56.100", "")
	t.Logf("Country: %s", noGeoDB)
	invalidIP := GetCountryByIPLocal("192.168.56.300", "GeoLite2-Country.mmdb")
	t.Logf("Country: %s", invalidIP)
}
