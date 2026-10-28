package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                string
	DiscordBotToken     string
	DiscordClientID     string
	DiscordClientSecret string
	DiscordRedirectURI  string
	JWTSecret           string
	DataPath            string
	IsDemoMode          bool
}

func Load() *Config {
	_ = godotenv.Load(".env", "backend/.env", "../.env")

	port := getEnv("PORT", "8080")
	token := os.Getenv("DISCORD_BOT_TOKEN")
	clientID := os.Getenv("DISCORD_CLIENT_ID")
	clientSecret := os.Getenv("DISCORD_CLIENT_SECRET")
	redirectURI := getEnv("DISCORD_REDIRECT_URI", "http://localhost:5173/auth/callback")
	jwtSecret := getEnv("JWT_SECRET", "super-secret-jwt-key-for-discord-bot")
	dataPath := getEnv("DATA_PATH", "./data.json")

	isDemo := false
	if strings.TrimSpace(token) == "" || strings.ToLower(strings.TrimSpace(token)) == "demo" {
		isDemo = true
	}

	return &Config{
		Port:                port,
		DiscordBotToken:     token,
		DiscordClientID:     clientID,
		DiscordClientSecret: clientSecret,
		DiscordRedirectURI:  redirectURI,
		JWTSecret:           jwtSecret,
		DataPath:            dataPath,
		IsDemoMode:          isDemo,
	}
}

func getEnv(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}
