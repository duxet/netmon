package common

import (
	"github.com/oschwald/geoip2-golang"
	"log"
)

func GetCountryCode(ipAddress IPAddress) *string {
	db, err := geoip2.Open("GeoLite2-Country.mmdb")
	if err != nil {
		log.Println("Failed to open GeoIP2-Country.mmdb", err)
		return nil
	}

	country, err := db.Country(ipAddress.AsSlice())
	if err != nil {
		log.Println("Failed to get country", err)
	}

	return &country.Country.IsoCode
}
