package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const cacheDir = "cache"
const cacheTimeHours = 23
const lockFile = ".lock"
const overpassDataFile = "overpass.json"
const overpassURL = "http://overpass-api.de/api/interpreter?data="
const vvrDataFile = "vvr.json"
const vvrSearchURL = "https://vvr.verbindungssuche.de/fpl/suhast.php?&query="

// between prefix and suffix a list of cities divided only by a pipe | is expected
const overpassQueryPrefix = "[out:json][timeout:600];area[boundary=administrative][admin_level=8][name~'("
const overpassQuerySuffix = ")']->.searchArea;(nw[\"highway\"=\"bus_stop\"](area.searchArea);node[\"public_transport\"=\"stop_position\"](area.searchArea););out;"

// flags
var debug = flag.Bool("d", false, "get debug output (implies verbose mode)")
var verbose = flag.Bool("verbose", false, "verbose mode")

// non-const consts
var cities = [...]string{"Altefähr", "Kramerhof", "Parow", "Prohn", "Stralsund"}
var httpClient = &http.Client{Timeout: 10 * time.Second}

// type definitions
// VvrBusStop represents all info from VVR belonging to one bus stop
type VvrBusStop struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Label string `json:"label"`
}

// VvrCity holds the VVR data and some meta data about it
type VvrCity struct {
	SearchWord      string
	ResultTimeStamp time.Time
	Result          []VvrBusStop
}

// VvrData holds the VVR data, the OSM data and some meta data for one city
type VvrData struct {
	CityResults []VvrCity
}

type OsmElement struct {
	Type string  `json:"type"`
	ID   int     `json:"id"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Tags struct {
		Bench            string `json:"bench"`
		Bin              string `json:"bin"`
		Bus              string `json:"bus"`
		CheckDateShelter string `json:"check_date:shelter"`
		DeparturesBoard  string `json:"departures_board"`
		Highway          string `json:"highway"`
		Lit              string `json:"lit"`
		Name             string `json:"name"`
		Operator         string `json:"operator"`
		PublicTransport  string `json:"public_transport"`
		Shelter          string `json:"shelter"`
		TactilePaving    string `json:"tactile_paving"`
		Wheelchair       string `json:"wheelchair"`
	} `json:"tags,omitempty"`
}

// OverpassData holds the OSM data queried via overpass api
type OverpassData struct {
	Version   float64 `json:"version"`
	Generator string  `json:"generator"`
	Osm3S     struct {
		TimestampOsmBase   time.Time `json:"timestamp_osm_base"`
		TimestampAreasBase time.Time `json:"timestamp_areas_base"`
		Copyright          string    `json:"copyright"`
	} `json:"osm3s"`
	Elements []OsmElement `json:"elements"`
}

// MatchedBusStops is a result of the merge of VVR data with OSM data
type MatchedBusStop struct {
	Name     string
	VvrID    string
	City     string
	Elements []OsmElement
}

type MatchResult struct {
	Name  string
	VvrID string
}

// functions
func removeLockFile(lf string) {
	if *debug {
		log.Printf("removeLockFile: trying to delete %s\n", lf)
	}
	err := os.Remove(lf)
	if err != nil {
		log.Printf("removeLockFile: error while removing lock file %s\n", lf)
		log.Panic(err)
	}
}

func readCurrentJSON(i interface{}) error {
	if *debug {
		log.Println("readCurrentJSON")
	}
	var jsonFilePath string
	if *debug {
		log.Println("readCurrentJSON: given type:")
		log.Printf("%T\n", i)
	}
	switch i.(type) {
	case *VvrData:
		if *debug {
			log.Println("readCurrentJSON: found *VvrData type")
		}
		jsonFilePath = cacheDir + string(os.PathSeparator) + vvrDataFile
	case *OverpassData:
		if *debug {
			log.Println("readCurrentJSON: found *OverpassData type")
		}
		jsonFilePath = cacheDir + string(os.PathSeparator) + overpassDataFile

	default:
		log.Fatalln("readCurrentJSON: unkown type for reading json")
		return nil
	}

	if *debug {
		log.Println("readCurrentJSON: jsonFilePath is", jsonFilePath)
	}
	if _, err := os.Stat(jsonFilePath); os.IsNotExist(err) {
		// in case file does not exist, we cannot prefill the data from json
		if *verbose { // not fatal, just start with a new one
			log.Printf("file does not exist %s\n", jsonFilePath)
		}
		return nil
	}
	b, err := ioutil.ReadFile(jsonFilePath)
	if err != nil {
		if *debug {
			log.Println("readCurrentJSON: error while ioutil.ReadFile", err)
		}
		fmt.Println(err)
		return err
	}
	err = json.Unmarshal(b, i)
	if err != nil {
		if *debug {
			log.Println("readCurrentJSON: error while json.Unmarshal", err)
		}
		return err
	}
	return nil
}

func writeNewJSON(i interface{}) error {
	if *debug {
		log.Println("writeNewJSON: given type:")
		log.Printf("%T\n", i)
	}
	var jsonFilePath string
	switch i.(type) {
	case VvrData:
		if *debug {
			log.Println("found VvrData type")
		}
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			os.Mkdir(cacheDir, os.ModePerm)
		}
		jsonFilePath = cacheDir + string(os.PathSeparator) + vvrDataFile
	default:
		return errors.New("unkown data type for writing json")
	}
	b, err := json.Marshal(i)
	if err != nil {
		if *debug {
			log.Println("writeNewJSON: error while marshalling data json", err)
		}
		return err
	}
	err = ioutil.WriteFile(jsonFilePath, b, 0644)
	if err != nil {
		if *debug {
			log.Println("writeNewJSON: error while writing data json", err)
		}
		return err
	}
	return nil
}

func getJson(url string, target interface{}) error {
	r, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func printElapsedTime(start time.Time) {
	if *debug {
		log.Printf("printElapsedTime: time elapsed %.2fs\n", time.Since(start).Seconds())
	}
}

func getCityResultFromData(cityName string, vvr VvrData) *VvrCity {
	for i := 0; i < len(vvr.CityResults); i++ {
		if vvr.CityResults[i].SearchWord == cityName {
			return &vvr.CityResults[i]
		}
	}
	return nil
}

func getOverpassQueryURL() string {
	citiesPiped := ""
	for i := 0; i < len(cities); i++ {
		citiesPiped = citiesPiped + cities[i]
		if i+1 != len(cities) {
			citiesPiped = citiesPiped + "|"
		}
	}
	return overpassURL + overpassQueryPrefix + citiesPiped + overpassQuerySuffix
}

func doesOsmElementMatchVvrElement(osm OsmElement, name string, city string) bool {
	// TODO use city to find more false negatives
	return osm.Tags.Name == name
}

func main() {
	start := time.Now()
	defer printElapsedTime(start)

	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Flag handling
	flag.Parse()
	if *debug && len(flag.Args()) > 0 {
		log.Printf("non-flag args=%v\n", strings.Join(flag.Args(), " "))
	}

	if *verbose && !*debug {
		log.Println("verbose mode")
	}
	if *debug {
		log.Println("debug mode")
		// debug implies verbose
		*verbose = true
	}

	// check if lock file exists and exit, so we do not run this process two times
	if _, err := os.Stat(lockFile); os.IsNotExist(err) {
		if *debug {
			log.Printf("no lockfile %s present\n", lockFile)
		}
	} else {
		fmt.Printf("abort: lock file exists %s\n", lockFile)
		os.Exit(1)
	}

	// create lock file and delete it on exit of main
	err := ioutil.WriteFile(lockFile, nil, 0644)
	if err != nil {
		if *debug {
			log.Println("main: error while writing lock file")
		}
		panic(err)
	}
	defer removeLockFile(lockFile)

	if *verbose {
		log.Println("reading data json file into memory")
	}
	var oldVvr VvrData
	err = readCurrentJSON(&oldVvr)
	if err != nil {
		removeLockFile(lockFile)
		panic(err)
	}

	var newVvr VvrData
	for i := 0; i < len(cities); i++ {
		var newVvrCity VvrCity
		var newResult []VvrBusStop
		isNewApiCallNeeded := false
		oldVvrCity := getCityResultFromData(cities[i], oldVvr)
		if oldVvrCity != nil {
			if *debug {
				log.Printf("found old result for %s, checking for timestamp %s\n", oldVvrCity.SearchWord, oldVvrCity.ResultTimeStamp)
			}
			cacheTime := time.Now().Add(-1 * cacheTimeHours * time.Hour)
			if oldVvrCity.ResultTimeStamp.Before(cacheTime) {
				if *debug {
					log.Printf("data in cache is older than %d hours, trying to get fresh data\n", cacheTimeHours)
				}
				isNewApiCallNeeded = true
			} else {
				if *debug {
					log.Printf("reusing data from cache (cause it's not older than %d hours)\n", cacheTimeHours)
				}
			}
		} else {
			isNewApiCallNeeded = true
		}
		if isNewApiCallNeeded {
			getURL := vvrSearchURL + cities[i]
			err = getJson(getURL, &newResult)
			if err != nil {
				log.Println("error getting http json for", getURL)
				log.Println("error is", err)
				if oldVvrCity != nil {
					log.Printf("reusing old cache data for %s due to the GET error\n", cities[i])
					newVvr.CityResults = append(newVvr.CityResults, *oldVvrCity)
				}
				continue
			}
			newVvrCity.SearchWord = cities[i]
			newVvrCity.ResultTimeStamp = time.Now()
			newVvrCity.Result = newResult
			newVvr.CityResults = append(newVvr.CityResults, newVvrCity)
		} else {
			newVvr.CityResults = append(newVvr.CityResults, *oldVvrCity)
		}
	}
	log.Println(newVvr)
	err = writeNewJSON(newVvr)
	if err != nil {
		log.Printf("error writing json with VVR data: %v\n", err)
	}
	overpassQuery := getOverpassQueryURL()
	if *debug {
		log.Println("overpassQuery:", overpassQuery)
	}
	var oldOverpassData OverpassData
	err = readCurrentJSON(&oldOverpassData)
	if err != nil {
		removeLockFile(lockFile)
		panic(err)
	}
	log.Println("TimestampAreasBase:", oldOverpassData.Osm3S.TimestampAreasBase)
	log.Println("TimestampOsmBase:", oldOverpassData.Osm3S.TimestampOsmBase)
	// TOOD get fresh data from overpass

	// compare
	var mbs []MatchedBusStop
	// at first load all bus stops into
	insaneLoops := 0
	for i := 0; i < len(newVvr.CityResults); i++ {
		for k := 0; k < len(newVvr.CityResults[i].Result); k++ {
			oneBusStop := newVvr.CityResults[i].Result[k]
			var oneMatch MatchedBusStop
			oneMatch.Name = oneBusStop.Value
			oneMatch.VvrID = oneBusStop.ID
			oneMatch.City = newVvr.CityResults[i].SearchWord
			for m := 0; m < len(oldOverpassData.Elements); m++ {
				if doesOsmElementMatchVvrElement(oldOverpassData.Elements[m], oneMatch.Name, oneMatch.City) {
					oneMatch.Elements = append(oneMatch.Elements, oldOverpassData.Elements[m])
				} else {
					var notInVvrButInOsm MatchedBusStop
					notInVvrButInOsm.City = newVvr.CityResults[i].SearchWord
					notInVvrButInOsm.Name = oldOverpassData.Elements[m].Tags.Name
					notInVvrButInOsm.Elements = append(notInVvrButInOsm.Elements, oldOverpassData.Elements[m])
					mbs = append(mbs, notInVvrButInOsm)
				}
				insaneLoops++
			}
			mbs = append(mbs, oneMatch)
		}
	}
	if *debug {
		log.Println("insane looping finished:", insaneLoops)
	}

}