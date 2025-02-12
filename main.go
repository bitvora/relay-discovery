package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/fiatjaf/eventstore"
	"github.com/fiatjaf/eventstore/lmdb"
	"github.com/fiatjaf/khatru"
	"github.com/joho/godotenv"
	"github.com/nbd-wtf/go-nostr"
)

type Config struct {
	RelayName        string
	RelayPubkey      string
	RelayDescription string
	RelayIcon        string
	RelaySoftware    string
	RelayVersion     string
	RelayPort        string
	LmdbMapSize      int64
	LmdbPath         string
	GlobalRelays     []string
}

var config Config
var pool *nostr.SimplePool
var db *lmdb.LMDBBackend
var relay *khatru.Relay
var relayKinds = []int{nostr.KindRelayListMetadata, nostr.KindRelaySets, nostr.KindRelayReviews, nostr.KindBlockedRelayList, nostr.KindDMRelayList, nostr.KindSearchRelayList, nostr.KindGoodWikiRelayList}

func main() {
	LoadConfig()
	db = newLMDBBackend(config.LmdbPath)

	if err := db.Init(); err != nil {
		panic(err)
	}

	relay = khatru.NewRelay()
	relay.Info.Name = config.RelayName
	relay.Info.PubKey = config.RelayPubkey
	relay.Info.Description = config.RelayDescription
	relay.Info.Icon = config.RelayIcon
	relay.Info.Software = config.RelaySoftware
	relay.Info.Version = config.RelayVersion

	pool = nostr.NewSimplePool(context.Background())

	go SubscribeGlobal()

	relay.StoreEvent = append(relay.StoreEvent, db.SaveEvent)
	relay.QueryEvents = append(relay.QueryEvents, db.QueryEvents)

	relay.RejectFilter = append(relay.RejectFilter, func(ctx context.Context, filter nostr.Filter) (bool, string) {
		if !containsOnlyRelayKinds(filter.Kinds) {
			return true, "invalid-filter: filter contains kinds that aren't relay kinds"
		}

		return false, ""
	})

	relay.RejectEvent = append(relay.RejectEvent, func(ctx context.Context, event *nostr.Event) (bool, string) {
		if !containsOnlyRelayKinds([]int{event.Kind}) {
			return true, "invalid-event: event isn't a relay kind"
		}

		return false, ""
	})

	addr := fmt.Sprintf("%s:%s", "0.0.0.0", config.RelayPort)

	log.Printf("🔗 listening at %s", addr)
	http.ListenAndServe(addr, relay)

}

func containsOnlyRelayKinds(kinds []int) bool {
	relayKindSet := make(map[int]struct{})

	for _, relayKind := range relayKinds {
		relayKindSet[relayKind] = struct{}{}
	}
	for _, kind := range kinds {
		if _, exists := relayKindSet[kind]; !exists {
			return false
		}
	}

	return true
}

func SubscribeGlobal() {
	ctx := context.Background()
	wdb := eventstore.RelayWrapper{Store: db}

	filters := []nostr.Filter{{
		Kinds: relayKinds,
	}}

	for ev := range pool.SubMany(ctx, config.GlobalRelays, filters) {
		wdb.Publish(ctx, *ev.Event)
	}
}

func LoadConfig() {
	_ = godotenv.Load(".env")

	config = Config{
		RelayName:        os.Getenv("RELAY_NAME"),
		RelayPubkey:      os.Getenv("RELAY_PUBKEY"),
		RelayDescription: os.Getenv("RELAY_DESCRIPTION"),
		RelayIcon:        os.Getenv("RELAY_ICON"),
		RelaySoftware:    "https://github.com/bitvora/relay-discovery",
		RelayVersion:     "0.1.0",
		RelayPort:        os.Getenv("RELAY_PORT"),
		LmdbMapSize:      0,
		LmdbPath:         os.Getenv("LMDB_PATH"),
		GlobalRelays:     getRelayListFromFile(os.Getenv("GLOBAL_RELAYS")),
	}
}

func newLMDBBackend(path string) *lmdb.LMDBBackend {
	return &lmdb.LMDBBackend{
		Path:    path,
		MapSize: config.LmdbMapSize,
	}
}

func getRelayListFromFile(filePath string) []string {
	file, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read file: %s", err)
	}

	var relayList []string
	if err := json.Unmarshal(file, &relayList); err != nil {
		log.Fatalf("Failed to parse JSON: %s", err)
	}

	for i, relay := range relayList {
		relay = strings.TrimSpace(relay)
		if !strings.HasPrefix(relay, "wss://") && !strings.HasPrefix(relay, "ws://") {
			relay = "wss://" + relay
		}
		relayList[i] = relay
	}
	return relayList
}
