/*! \file main.go
    \brief Main file/entry point for Toggle (toggle for redis)

    This app is designed to switch between two machines both running redis as a master/slave setup
    The goal is to be able to "toggle" back and forth between the machines.  When the "master" is no longer ping'able
    we'll issue a "slaveof no one" to the "slave".
    It will then swap the ip address of the nginx load balancer to point to the new "master" node
    The app will continue to attempt communication with the now down master, to issue a new "saveof" command
    to have it replicate from the new master.
    I've tested this and it appears it works well to coordinate quick switches using a loadbalancer and the redis
    instances are able to pick up where they left off and keep going

    do a 
    kill -10 pid
    to cause this to switch between the master and slave

*   2017-09-15 NT   Created
    2017-12-29 NT   Modified so there's a master who can be return a json of the current setup to a request
                    Which allows a slave to copy the current master's setup
                    This allows multiple load-balancers
_ = "breakpoint"
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

const API_VER = "0.2.0"
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
    if len(config.Master.PublicIP) < 1 { config.Master.PublicIP = config.Master.PrivateIP}
    if len(config.Slave.PublicIP) < 1 { config.Slave.PublicIP = config.Slave.PrivateIP}
    if len(config.Master.PrivateIP) < 1 { config.Master.PrivateIP = config.Master.PublicIP}
    if len(config.Slave.PrivateIP) < 1 { config.Slave.PrivateIP = config.Slave.PublicIP}

    if len(config.Master.PublicIP) < 7 { //not a real ip check, but we need something to verify it looks good
        log.Fatalln("Master ip from redis server appears invalid")
    }
    if len(config.Slave.PublicIP) < 7 { //not a real ip check, but we need something to verify it looks good
        log.Fatalln("Slave ip from redis server index appears invalid")
    }
    if config.Master.Port < 1 {
        log.Fatalln("Masterport from redis server index appears invalid")
    }
    if config.Slave.Port < 1 {
        log.Fatalln("Masterport from redis server index appears invalid")
    }
}

func writeConfig (config *appConfig_t, fileLoc string) {
    fmt.Println("writing new config")
    byt, _ := json.Marshal(*config)
    err := ioutil.WriteFile(fileLoc, byt, 0666)
    if err != nil { log.Println(err) }
}

func masterEndpoint(w http.ResponseWriter, r *http.Request) {
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
	intervalFlag := flag.Int("i", 5, "Interval in seconds to check if the master is alive")
    retryFlag := flag.Int("r", 2, "Interval in seconds to double check if the master is alive")
    configFlag := flag.String("c", "toggle.conf", "Location of the config file")
    portFlag := flag.Int("p", 0, "Port for this master to return the current status. Also makes this act as a master")
    slaveFlag := flag.Bool("slave", false, "Makes this instance run as a slave, only polls for changes, won't make them")
    masterIPFlag := flag.String("master", "", "ip address of the master toggle service we're going to ask the settings of")
	
	flag.Parse()

	if *versionFlag {
		fmt.Printf("\nToggle Version: %s\n\n", API_VER)
		os.Exit(0)
    } 

    defer log.Println("Toggle Toggle MuthaF*cker")
    wg := new(sync.WaitGroup)  //use this to control the exiting of everything
    wg.Add(1)   //add 1 to our group, we can do everything in the background using a single other thread
    
    //signals for quitting
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

    //tickers for the tasks scheduled at intervals
    ticker := time.NewTicker(time.Second * time.Duration(*intervalFlag))    //used for the master as well as the slave
    
    go func() { //handle exiting
        <-c
        //for sig := range c {
        // sig is a ^C, handle it
        log.Println("Toggle stopping")

        ticker.Stop()   //this kills the above thread loop
        wg.Done()   //if we're here it's cause the ticker is no more
        //}
    }()

    if *slaveFlag {  //we're running as a slave, this is different.  
        //We only poll the other server for the current master and copy the settings here
        if *portFlag == 0 { log.Fatalln("Slave must have -p= set to the port the master is running on") }
        if len(*masterIPFlag) < 7 { log.Fatalln("Master ip [--master=] appears invalid") }

        //slave task
        go func() {
            lastIP := ""
            lastPort := 0
            tasks := tasks_c{}
            nginx := nginx.Nginx_c{}

            for range ticker.C {  //every time we "tick"
                ip, port, err := tasks.SlaveCheck (*masterIPFlag, *portFlag)
                if err == nil { //otherwise we ignore this
                    if lastIP != ip || lastPort != port {
                        log.Printf("Slave set config to %s:%d\n", ip, port)
                        nginx.Set (ip, port)    //update nginx to reflect this new setup
                        lastIP, lastPort = ip, port //it changed, so save it
                    }
                }
            }
        }()

        wg.Wait() //wait here until we get an exit ^C request
        os.Exit(0)  //we're done
	} else if *intervalFlag < 1 {
        log.Fatalf("Interval time is invalid, must be greater than 0: %d\n", *intervalFlag)
    } else if *retryFlag < 1 {
        log.Fatalf("retry time is invalid, must be greater than 0: %d\n", *intervalFlag)
    }

	loadConfig(&appConfig, *configFlag) //load our config file
    tasks := tasks_c{Config: &appConfig, Retry: *retryFlag} //this "class" handles the actual work, we just need to call it when it's appropriate
    
    //first we want to validate our config so that tasks can run when we schedule it to
    tasks.ValidateConfig()  //if we don't throw a fatal, then we can keep going here

    //signal for switching master/slave
    switchSignal := make(chan os.Signal, 1)
    signal.Notify(switchSignal, syscall.SIGUSR1)
	
    //master task
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
            log.Println("Toggle running as master on port : ", *portFlag)
            http.HandleFunc("/", masterEndpoint)
            http.ListenAndServe(fmt.Sprintf(":%d", *portFlag), nil)
        }()
    }
	
	wg.Wait() //wait here until we get an exit ^C request
}
