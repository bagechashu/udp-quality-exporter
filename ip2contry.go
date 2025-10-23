package main

import (
	"net"

	"github.com/oschwald/geoip2-golang"
)

func GetCountryByIPLocal(ipStr, geoipDbPath string) string {
	db, err := geoip2.Open(geoipDbPath)
	if err != nil {
		return "noGeoDB"
	}
	defer db.Close()

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "invalidIP"
	}

	record, err := db.Country(ip)
	if err != nil {
		return "unknown"
	}

	country := record.Country.IsoCode
	if country == "" {
		country = "bogon"
	}
	return country
}
