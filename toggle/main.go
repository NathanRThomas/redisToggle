/*! \file main.go
    \brief Main file/entry point for Toggle (toggle for redis)

    This app is designed to switch between two machines both running redis as a main/subordinate setup
    The goal is to be able to "toggle" back and forth between the machines.  When the "main" is no longer ping'able
    we'll issue a "subordinateof no one" to the "subordinate".
    It will then swap the ip address of the nginx load balancer to point to the new "main" node
    The app will continue to attempt communication with the now down main, to issue a new "saveof" command
    to have it replicate from the new main.
    I've tested this and it appears it works well to coordinate quick switches using a loadbalancer and the redis
    instances are able to pick up where they left off and keep going

    do a 
    kill -10 pid
    to cause this to switch between the main and subordinate

*   2017-09-15 NT   Created
    2017-12-29 NT   Modified so there's a main who can be return a json of the current setup to a request
                    Which allows a subordinate to copy the current main's setup
                    This allows multiple load-balancers

2018-07-05  NT  
    Redid config file and application to be a little more simple.  We know only have 2 servers, and they can have any number of
    redis ports between them.  The concept is that if a server is bad, we assume that all ports are bad and we switch all of them

    Also this will now attempt to set a value in the database, to ensure that the server is actually running as the main.  If it fails
    to set the value, then it will attempt to re-instate that server/port as the main
*/

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
    "io/ioutil"
    "net/http"

    "github.com/NathanRThomas/redisToggle/nginx"
)

const API_VER = "0.2.1"
var appConfig appConfig_t //create an instance of our app config

//---------------------------------------------------------------------------------------------------------------------------//
//----- PRIVATE FUNCTIONS -------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

func loadConfig (config *appConfig_t, fileLoc string) {
    configFile, err := os.Open(fileLoc) //try the file

    if err == nil {
        jsonParser := json.NewDecoder(configFile)
        err = jsonParser.Decode(config)
    }
    
    if err != nil {
        log.Fatalln(err)    //we can't move forward from here no matter what
    }

    //validate the file makes sense
    //validate each redis server config, we'll actually try to resolve them later
    if len(config.Main.PublicIP) < 1 { config.Main.PublicIP = config.Main.PrivateIP}
    if len(config.Subordinate.PublicIP) < 1 { config.Subordinate.PublicIP = config.Subordinate.PrivateIP}
    if len(config.Main.PrivateIP) < 1 { config.Main.PrivateIP = config.Main.PublicIP}
    if len(config.Subordinate.PrivateIP) < 1 { config.Subordinate.PrivateIP = config.Subordinate.PublicIP}

    if len(config.Main.PublicIP) < 7 { //not a real ip check, but we need something to verify it looks good
        log.Fatalln("Main ip from redis server appears invalid")
    }
    if len(config.Subordinate.PublicIP) < 7 { //not a real ip check, but we need something to verify it looks good
        log.Fatalln("Subordinate ip from redis server index appears invalid")
    }
    if len(config.Ports) < 1 {
        log.Fatalln("No ports are setup in the config")
    }
}

func writeConfig (config *appConfig_t, fileLoc string) {
    fmt.Println("writing new config")
    byt, _ := json.Marshal(*config)
    err := ioutil.WriteFile(fileLoc, byt, 0666)
    if err != nil { log.Println(err) }
}

func mainEndpoint(w http.ResponseWriter, r *http.Request) {
    if r.Method == "OPTIONS" { return } //this is a "test" request sent by javascript to test if the call is valid, or something, so just ignore it
    js, _ := json.Marshal(appConfig)
    w.Write(js)
}


//-------------------------------------------------------------------------------------------------------------------------//
//----- MAIN --------------------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

func main() {
    log.SetFlags(log.LstdFlags | log.Lshortfile) //configure the logging for this application
	
	versionFlag := flag.Bool("v", false, "Returns the version")
	intervalFlag := flag.Int("i", 2, "Interval in seconds to check if the main is alive")
    retryFlag := flag.Int("r", 2, "Interval in seconds to double check if the main is alive")
    configFlag := flag.String("c", "toggle.conf", "Location of the config file")
    portFlag := flag.Int("p", 0, "Port for this main to return the current status. Also makes this act as a main")
    subordinateFlag := flag.Bool("subordinate", false, "Makes this instance run as a subordinate, only polls for changes, won't make them")
    mainIPFlag := flag.String("main", "", "ip address of the main toggle service we're going to ask the settings of")
    testFlag := flag.Bool("testing", false, "Creates the configs and writes to the log, but doesn't actually change anything.  Used for testing")
	
	flag.Parse()

	if *versionFlag {
		fmt.Printf("\nToggle Version: %s\n\n", API_VER)
		os.Exit(0)
    } else if *intervalFlag < 1 {
        log.Fatalf("Interval time is invalid, must be greater than 0: %d\n", *intervalFlag)
    } else if *retryFlag < 1 {
        log.Fatalf("retry time is invalid, must be greater than 0: %d\n", *intervalFlag)
    }

    defer log.Println("Toggle Toggle MuthaF*cker")
    wg := new(sync.WaitGroup)  //use this to control the exiting of everything
    wg.Add(1)   //add 1 to our group, we can do everything in the background using a single other thread
    
    //signals for quitting
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

    //tickers for the tasks scheduled at intervals
    ticker := time.NewTicker(time.Second * time.Duration(*intervalFlag))    //used for the main as well as the subordinate
    
    go func() { //handle exiting
        <-c
        //for sig := range c {
        // sig is a ^C, handle it
        log.Println("Toggle stopping")

        ticker.Stop()   //this kills the above thread loop
        wg.Done()   //if we're here it's cause the ticker is no more
        //}
    }()

    if *subordinateFlag {  //we're running as a subordinate, this is different.  
        //We only poll the other server for the current main and copy the settings here
        if *portFlag == 0 { log.Fatalln("Subordinate must have -p= set to the port the main is running on") }
        if len(*mainIPFlag) < 7 { log.Fatalln("Main ip [--main=] appears invalid") }

        //subordinate task
        go func() {
            lastIP := ""
            tasks := tasks_c{}
            nginx := nginx.Nginx_c{ TestingFlag: *testFlag }

            for range ticker.C {  //every time we "tick"
                config, err := tasks.SubordinateCheck (*mainIPFlag, *portFlag)
                if err == nil { //otherwise we ignore this
                    if lastIP != config.Main.PrivateIP {  //we've had a switch
                        log.Printf("Subordinate set config to %s\n", config.Main.PrivateIP)
                        nginx.Set (config.Main.PrivateIP, config.Ports)    //update nginx to reflect this new setup
                        lastIP = config.Main.PrivateIP //it changed, so save it
                    }
                } else {
                    log.Println(err)    //we had an error
                }
            }
        }()

        wg.Wait() //wait here until we get an exit ^C request
        os.Exit(0)  //we're done
	}

	loadConfig(&appConfig, *configFlag) //load our config file
    tasks := tasks_c{Config: &appConfig, Retry: *retryFlag, TestingFlag: *testFlag} //this "class" handles the actual work, we just need to call it when it's appropriate
    
    //first we want to validate our config so that tasks can run when we schedule it to
    tasks.ValidateConfig()  //if we don't throw a fatal, then we can keep going here

    //signal for switching main/subordinate
    switchSignal := make(chan os.Signal, 1)
    signal.Notify(switchSignal, syscall.SIGUSR1)
	
    //main task
	go func() {
        for range ticker.C {  //every time we "tick"
            if tasks.Check() {    //main entry point
                writeConfig (&appConfig, *configFlag)
            }
		}
	}()

    go func() {
        <-switchSignal

        log.Println("Switching due to signal")
        if tasks.Switch() {
            writeConfig (&appConfig, *configFlag)
        }
    }()

    if *portFlag > 0 {
        go func() {
            log.Println("Toggle running as main on port : ", *portFlag)
            http.HandleFunc("/", mainEndpoint)
            http.ListenAndServe(fmt.Sprintf(":%d", *portFlag), nil)
        }()
    }
	
	wg.Wait() //wait here until we get an exit ^C request
}
