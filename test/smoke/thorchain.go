package smoke

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"time"

	ctypes "github.com/binance-chain/go-sdk/common/types"
	"github.com/pkg/errors"

	"gitlab.com/thorchain/thornode/test/smoke/types"
)

var endpoints = map[string]string{
	"ci":         "docker:1317",
	"local":      "localhost:1317",
	"mocknet":    "67.205.166.241:1317",
	"staging":    "testnet-chain.bepswap.io",
	"develop":    "testnet-chain.bepswap.net",
	"production": "testnet-chain.bepswap.com",
}

type Thorchain struct {
	Env string
}

// NewThorchain : Create a new Thorchain instance.
func NewThorchain(env string) Thorchain {
	return Thorchain{
		Env: env,
	}
}

// WaitForAvailability - pings thorchain until its available
func (s Thorchain) WaitForAvailability() {
	var count int
	for {
		fmt.Println("Waiting for thorchain availability")
		addr, err := s.PoolAddress()
		if err == nil {
			fmt.Printf("Pool Address found: %s\n", addr)
			break
		}
		fmt.Printf("Pool Address error: %s\n", err)
		count += 1
		if count > 35 {
			fmt.Println("Timeout: thorchain is unavailable")
			os.Exit(1)
		}
		time.Sleep(10 * time.Second)
	}
}

func (s Thorchain) PoolAddress() (ctypes.AccAddress, error) {
	// TODO : Fix this - this is a hack to get around the 1 query per second REST API limit.
	time.Sleep(1 * time.Second)
	ctypes.Network = ctypes.TestNetwork

	var addrs types.ThorchainPoolAddress

	resp, err := http.Get(s.PoolAddressesURL())
	if err != nil {
		return nil, errors.Wrap(err, "Failed getting thorchain")
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "Failed reading body")
	}

	if err := json.Unmarshal(data, &addrs); err != nil {
		return nil, errors.Wrap(err, "Failed to unmarshal pool addresses")
	}

	if len(addrs.Current) == 0 {
		return nil, errors.New("No pool addresses are currently available")
	}
	poolAddr := addrs.Current[0]

	addr, err := ctypes.AccAddressFromBech32(poolAddr.Address.String())
	if err != nil {
		return nil, errors.Wrap(err, "Failed to parse address")
	}

	return addr, nil
}

// GetThorchain : Get the Statehcain pools.
func (s Thorchain) GetPools() types.ThorchainPools {
	// TODO : Fix this - this is a hack to get around the 1 query per second REST API limit.
	time.Sleep(1 * time.Second)

	var pools types.ThorchainPools

	resp, err := http.Get(s.PoolURL())
	if err != nil {
		log.Fatalf("Failed getting thorchain: %v\n", err)
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed reading body: %v\n", err)
	}

	if err := json.Unmarshal(data, &pools); err != nil {
		log.Fatalf("Failed to unmarshal pools: %s", err)
	}

	return pools
}

func (s Thorchain) GetHeight() int {
	// TODO : Fix this - this is a hack to get around the 1 query per second REST API limit.
	time.Sleep(1 * time.Second)

	var block types.LastBlock

	resp, err := http.Get(s.BlockURL())
	if err != nil {
		log.Fatalf("Failed getting thorchain: %v\n", err)
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed reading body: %v\n", err)
	}

	if err := json.Unmarshal(data, &block); err != nil {
		log.Fatalf("Failed to unmarshal pools: %s", err)
	}

	height, _ := strconv.Atoi(block.Height)
	return height
}

func (s Thorchain) getUrl(p string) string {
	scheme := "https"
	if s.Env == "local" || s.Env == "ci" || s.Env == "mocknet" {
		scheme = "http"
	}
	u := url.URL{
		Scheme: scheme,
		Host:   endpoints[s.Env],
		Path:   path.Join("thorchain", p),
	}
	return u.String()
}

func (s Thorchain) BlockURL() string {
	return s.getUrl("/lastblock")
}

// PoolURL : Return the Pool URL based on the selected environment.
func (s Thorchain) PoolURL() string {
	return s.getUrl("/pools")
}

// StakerURL  : Return the Staker URL based on the selected environment.
func (s Thorchain) StakerURL(staker string) string {
	return s.getUrl(fmt.Sprintf("/staker/%s", staker))
}

func (s Thorchain) PoolAddressesURL() string {
	return s.getUrl("/pool_addresses")
}
