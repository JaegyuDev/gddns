package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "time"

    cloudflare "github.com/cloudflare/cloudflare-go"
    "github.com/joho/godotenv"
)

// devMode is injected at build time via:
//
//	go build -ldflags="-X main.devMode=false" -o gddns
var devMode = "true"
var dataPath string

type Config struct {
    CfgFile
    Env struct {
        CFEmail  string
        CFApiKey string
        SysIP    string
    }
}

type CfgFile struct {
    Domain     string `json:"domain"`
    CNAME      string `json:"cname"`
    ZoneID     string `json:"zone_id"`
    RecordID   string `json:"record_id"`
    LastIP     string `json:"last_ip,omitempty"`
    PreferIPv6 bool   `json:"prefer_ipv6"`
}

// recordType returns the DNS record type appropriate for the resolved IP.
// IPv6 addresses contain colons; everything else is treated as IPv4.
func (c *Config) recordType() string {
    if strings.Contains(c.Env.SysIP, ":") {
        return "AAAA"
    }
    return "A"
}

func fetchIP(url string) (string, error) {
    resp, err := http.Get(url)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    ip, err := io.ReadAll(resp.Body)
    return strings.TrimSpace(string(ip)), err
}

// resolvePublicIP attempts to get the public IP according to the PreferIPv6
// setting. If IPv6 is preferred but unavailable, it falls back to IPv4 and
// logs a warning. If IPv4 is preferred, it is used directly with no fallback.
func resolvePublicIP(preferIPv6 bool) (string, error) {
    if preferIPv6 {
        ip, err := fetchIP("https://api6.ipify.org?format=text")
        if err == nil {
            return ip, nil
        }
        fmt.Printf("Warning: IPv6 lookup failed (%v), falling back to IPv4.\n", err)
    }
    return fetchIP("https://api.ipify.org?format=text")
}

func loadConfigAndEnv(filename string) (*Config, error) {
    file, err := os.Open(filename)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    var config Config
    if err := json.NewDecoder(file).Decode(&config.CfgFile); err != nil {
        return nil, fmt.Errorf("error decoding config: %w", err)
    }

    config.Env.CFApiKey = os.Getenv("CF_API_KEY")
    config.Env.CFEmail = os.Getenv("CF_EMAIL")
    if config.Env.CFApiKey == "" || config.Env.CFEmail == "" {
        return nil, fmt.Errorf("CF_API_KEY and CF_EMAIL must be set in environment")
    }

    ip, err := resolvePublicIP(config.PreferIPv6)
    if err != nil {
        return nil, fmt.Errorf("error getting public IP: %w", err)
    }
    config.Env.SysIP = ip

    return &config, nil
}

func saveConfig(config *Config) error {
    data, err := json.MarshalIndent(config.CfgFile, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(filepath.Join(dataPath, "config.json"), data, 0600)
}

func updateRecord(api *cloudflare.API, config *Config) error {
    recordParams := cloudflare.UpdateDNSRecordParams{
        ID:      config.RecordID,
        Type:    config.recordType(),
        Name:    config.CNAME,
        Content: config.Env.SysIP,
        TTL:     120,
        Comment: cloudflare.StringPtr("Automatically set by gddns"),
        Proxied: cloudflare.BoolPtr(false),
    }
    _, err := api.UpdateDNSRecord(context.Background(), cloudflare.ZoneIdentifier(config.ZoneID), recordParams)
    return err
}

// findRecord checks whether a matching record (A or AAAA depending on the
// resolved IP) already exists for the CNAME. If one is found, it populates
// config.RecordID so the caller can update rather than re-create the record.
func findRecord(api *cloudflare.API, config *Config) (bool, error) {
    records, _, err := api.ListDNSRecords(
        context.Background(),
        cloudflare.ZoneIdentifier(config.ZoneID),
        cloudflare.ListDNSRecordsParams{
            Type: config.recordType(),
            Name: config.CNAME,
        },
    )
    if err != nil {
        return false, err
    }

    if len(records) > 0 {
        config.RecordID = records[0].ID
        return true, nil
    }

    return false, nil
}

func createRecords(api *cloudflare.API, config *Config) error {
    cnameFull := config.CNAME + "." + config.Domain

    record, err := api.CreateDNSRecord(
        context.Background(),
        cloudflare.ZoneIdentifier(config.ZoneID),
        cloudflare.CreateDNSRecordParams{
            Type:    config.recordType(),
            Name:    config.CNAME,
            Content: config.Env.SysIP,
            TTL:     300,
            Proxied: cloudflare.BoolPtr(false),
            Comment: fmt.Sprintf("Automatically set by gddns at %s", time.Now().String()),
        },
    )
    if err != nil {
        return fmt.Errorf("error creating %s record: %w", config.recordType(), err)
    }

    _, err = api.CreateDNSRecord(
        context.Background(),
        cloudflare.ZoneIdentifier(config.ZoneID),
        cloudflare.CreateDNSRecordParams{
            Type: "SRV",
            Name: "_minecraft._tcp",
            Data: map[string]interface{}{
                "service":  "_minecraft",
                "proto":    "_tcp",
                "name":     cnameFull,
                "priority": 0,
                "weight":   5,
                "port":     25565,
                "target":   cnameFull,
            },
            TTL:     900,
            Proxied: cloudflare.BoolPtr(false),
            Comment: fmt.Sprintf("Automatically set by gddns at %s", time.Now().String()),
        },
    )
    if err != nil {
        return fmt.Errorf("error creating SRV record: %w", err)
    }

    config.RecordID = record.ID
    return nil
}

func setup() (*cloudflare.API, *Config, error) {
    config, err := loadConfigAndEnv(filepath.Join(dataPath, "config.json"))
    if err != nil {
        return nil, nil, fmt.Errorf("error loading configuration: %w", err)
    }

    api, err := cloudflare.New(config.Env.CFApiKey, config.Env.CFEmail)
    if err != nil {
        return nil, nil, fmt.Errorf("error initializing Cloudflare client: %w", err)
    }

    return api, config, nil
}

func run() error {
    api, config, err := setup()
    if err != nil {
        return fmt.Errorf("setup failed: %w", err)
    }

    // Skip API call entirely if the IP hasn't changed since last run.
    if config.LastIP == config.Env.SysIP {
        fmt.Println("IP unchanged, skipping update.")
        return nil
    }

    if config.RecordID != "" {
        if err := updateRecord(api, config); err != nil {
            return fmt.Errorf("error updating DNS record: %w", err)
        }
        fmt.Println("DNS record updated successfully.")
    } else {
        fmt.Println("No record ID set, checking Cloudflare for existing record...")

        found, err := findRecord(api, config)
        if err != nil {
            return fmt.Errorf("error checking existing DNS records: %w", err)
        }

        if found {
            fmt.Printf("Found existing record (ID: %s), updating...\n", config.RecordID)
            if err := updateRecord(api, config); err != nil {
                return fmt.Errorf("error updating recovered DNS record: %w", err)
            }
            fmt.Println("DNS record updated successfully.")
        } else {
            fmt.Println("No existing record found, creating new records...")
            if err := createRecords(api, config); err != nil {
                return fmt.Errorf("error creating DNS records: %w", err)
            }
            fmt.Println("DNS records created successfully.")
        }
    }

    // Persist the new IP and any updated record IDs.
    config.LastIP = config.Env.SysIP
    if err := saveConfig(config); err != nil {
        return fmt.Errorf("error saving config: %w", err)
    }
    fmt.Println("Config saved successfully.")

    return nil
}

func init() {
    if devMode == "true" {
        dataPath = "."
    } else {
        dataPath = "/etc/gddns"
    }
    fmt.Printf("Using data path: %s\n", dataPath)

    if err := godotenv.Load(filepath.Join(dataPath, ".env")); err != nil {
        log.Fatalf("Error loading .env file: %v", err)
    }
}

func main() {
    if err := run(); err != nil {
        log.Fatalf("Fatal: %v", err)
    }
}
