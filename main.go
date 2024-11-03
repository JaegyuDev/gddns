package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    cloudflare "github.com/cloudflare/cloudflare-go"
    "github.com/joho/godotenv"
    "io"
    "log"
    "net/http"
    "os"
    "strings"
    "time"
)

var setDevMode string
var dataPath string

type Config struct {
    *CfgFile
    Env struct {
        CFEmail  string
        CFApiKey string
        SysIP    string
    }
}

type CfgFile struct {
    Domain   string `json:"domain"`
    CNAME    string `json:"cname"`
    ZoneID   string `json:"zone_id"`
    RecordID string `json:"record_id"`
}

func getPublicIP() (string, error) {
    resp, err := http.Get("https://api.ipify.org?format=text")
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    ip, err := io.ReadAll(resp.Body)

    return string(ip), err
}

func loadConfigAndEnv(filename string) (*Config, error) {
    file, err := os.Open(filename)
    if err != nil {
        return nil, err
    }
    defer file.Close()
    var config Config
    if err := json.NewDecoder(file).Decode(&config); err != nil {
        return nil, err
    }

    config.Env.CFApiKey = os.Getenv("CF_API_KEY")
    config.Env.CFEmail = os.Getenv("CF_EMAIL")
    if config.Env.CFApiKey == "" || config.Env.CFEmail == "" {
        log.Fatal("Cloudflare API credentials are not set in environment variables.")
    }

    // Get current public IP
    ip, err := getPublicIP()
    if err != nil {
        log.Fatalf("Error getting public IP: %v", err)
    }
    config.Env.SysIP = ip

    return &config, nil
}

func saveConfig(config *Config) error {
    cfgdata := CfgFile{
        Domain:   config.Domain,
        CNAME:    config.CNAME,
        ZoneID:   config.ZoneID,
        RecordID: config.RecordID,
    }
    data, err := json.MarshalIndent(cfgdata, "", "  ")
    if err != nil {
        return err
    }

    return os.WriteFile(strings.Join([]string{dataPath, "config.json"}, "/"), data, 0600)
}

func updateRecord(api *cloudflare.API, config *Config) error {
    // Update DNS record
    recordParams := cloudflare.UpdateDNSRecordParams{
        ID:      config.RecordID,
        Type:    "A",
        Name:    config.CNAME,
        Content: config.Env.SysIP,
        TTL:     120, // Example TTL; change if necessary
        Comment: cloudflare.StringPtr("Automatically set by gddns"),
        Proxied: cloudflare.BoolPtr(false),
    }

    _, err := api.UpdateDNSRecord(context.Background(), cloudflare.ZoneIdentifier(config.ZoneID), recordParams)
    if err != nil {
        return err
    }

    return nil
}

func findRecord(api *cloudflare.API, config *Config) error {
    _, r, err := api.ListDNSRecords(context.Background(), cloudflare.ZoneIdentifier(config.ZoneID), cloudflare.ListDNSRecordsParams{
        Type: "A",
        Name: config.CNAME,
    })

    if err != nil {
        return err
    }

    if r.Count != 0 {
        return errors.New("record already exists")
    }

    return nil
}

func createRecords(api *cloudflare.API, config *Config) error {
    cnameFull := strings.Join([]string{config.CNAME, config.Domain}, ".")

    record, err := api.CreateDNSRecord(context.Background(), cloudflare.ZoneIdentifier(config.ZoneID), cloudflare.CreateDNSRecordParams{
        Type:    "A",
        Name:    config.CNAME,
        Content: config.Env.SysIP,
        TTL:     300,
        Proxied: cloudflare.BoolPtr(false),
        Comment: fmt.Sprintf("Automatically set by gddns at %s", time.Now().String()),
    })
    if err != nil {
        return err
    }

    _, err = api.CreateDNSRecord(context.Background(), cloudflare.ZoneIdentifier(config.ZoneID), cloudflare.CreateDNSRecordParams{
        Type: "SRV",
        Name: config.CNAME,
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
    })

    if err != nil {
        return err
    }

    config.RecordID = record.ID

    return nil
}

func setup() (api *cloudflare.API, config *Config, err error) {
    config, err = loadConfigAndEnv(strings.Join([]string{dataPath, "config.json"}, "/"))
    if err != nil {
        // Wrap the error with context, but do not log.Fatal
        return nil, nil, fmt.Errorf("error loading configuration: %w", err)
    }

    api, err = cloudflare.New(config.Env.CFApiKey, config.Env.CFEmail)
    if err != nil {
        return nil, nil, fmt.Errorf("error initializing Cloudflare client: %w", err)
    }

    return api, config, nil
}

func init() {
    if setDevMode == "false" {
        dataPath = "/etc/gddns"

    } else {
        dataPath = "."
    }
    fmt.Printf("Using data path: %s\n", dataPath)

    err := godotenv.Load(strings.Join([]string{dataPath, ".env.example"}, "/"))
    if err != nil {
        log.Fatalf("Error loading .env.example file: %v", err)
    }
}

func main() {
    api, config, err := setup()
    if err != nil {
        log.Fatalf("Setup failed: %v", err)
    }

    if config.RecordID != "" {
        if err := updateRecord(api, config); err != nil {
            log.Fatalf("Error updating DNS record: %v", err)
        }
        fmt.Println("DNS record updated successfully.")
        return
    }

    if config.RecordID == "" {
        fmt.Println("No DNS record ID was set...")
        err := findRecord(api, config)
        if err != nil {
            log.Fatalf("Error veryifying dns state: %v", err)
        }

        fmt.Println("new DNS record supplied, assuming new DNS record...")
        err = createRecords(api, config)
        if err != nil {
            log.Fatalf("Error creating records: %v", err)
        }

        fmt.Println("DNS record created successfully...")
        err = saveConfig(config)
        if err != nil {
            log.Fatalf("Error saving config: %v", err)
        }
        fmt.Println("DNS record saved successfully.")

        return
    }
}
