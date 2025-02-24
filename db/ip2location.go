package db

import (
	"fmt"

	"github.com/ip2location/ip2location-go/v9"
)

var IP2LDB *ip2location.DB = nil

func InitIP2LDB(dbpath string) error {
	var err error
	IP2LDB, err = ip2location.OpenDB(dbpath)
	if err != nil {
		return fmt.Errorf("failed to open ip2location lite db %v at %v", err, dbpath)
	}
	return nil
}

func GetPositionOfIP(ip string) (float32, float32, error) {
	results, err := IP2LDB.Get_all(ip)
	if err != nil {
		return 0, 0, err
	}
	return results.Longitude, results.Latitude, nil
}
