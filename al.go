package main

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println("Error loading .env file")
	}
	apiKey := os.Getenv("ALPACA_API_KEY")
	secret := os.Getenv("ALPACA_API_SECRET")

	url := "https://data.alpaca.markets/v2/stocks/bars?symbols=AAPL%2CTSLA&timeframe=1W&start=2024-05-14T00%3A00%3A00Z&end=2025-05-14T00%3A00%3A00Z&limit=1000&adjustment=raw&feed=sip&sort=asc"

	req, _ := http.NewRequest("GET", url, nil)

	req.Header.Add("accept", "application/json")
	req.Header.Add("APCA-API-KEY-ID", apiKey)
	req.Header.Add("APCA-API-SECRET-KEY", secret)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("Error sending request")
	}

	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	fmt.Println(string(body))
}
