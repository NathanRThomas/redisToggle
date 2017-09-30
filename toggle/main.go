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

*  2017-09-15 NT   Created
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
)

const API_VER = "0.1.0"

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
    if len(config.Redis) == 0 {
        log.Fatalln("No redis setups in the config file. Please see example config file")
    }

    //validate each redis server config, we'll actually try to resolve them later
    for idx, server := range(config.Redis) {
        if len(server.Master.PublicIP) < 1 { config.Redis[idx].Master.PublicIP = server.Master.PrivateIP}
        if len(server.Slave.PublicIP) < 1 { config.Redis[idx].Slave.PublicIP = server.Slave.PrivateIP}
    }

    for idx, server := range(config.Redis) {
        if len(server.Master.PublicIP) < 7 { //not a real ip check, but we need something to verify it looks good
            log.Fatalf("Master ip from redis server index %d appears invalid", idx)
        }
        if len(server.Slave.PublicIP) < 7 { //not a real ip check, but we need something to verify it looks good
            log.Fatalf("Slave ip from redis server index %d appears invalid", idx)
        }
        if server.Master.Port < 1 {
            log.Fatalf("Masterport from redis server index %d appears invalid", idx)
        }
        if server.Slave.Port < 1 {
            log.Fatalf("Masterport from redis server index %d appears invalid", idx)
        }
    }
}


//-------------------------------------------------------------------------------------------------------------------------//
//----- MAIN --------------------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

func main() {
    appConfig := appConfig_t {} //create an instance of our app config
	log.SetFlags(log.LstdFlags | log.Lshortfile) //configure the logging for this application
	
	versionFlag := flag.Bool("v", false, "Returns the version")
	intervalFlag := flag.Int("i", 10, "Interval in seconds to check if the master is alive")
    retryFlag := flag.Int("r", 2, "Interval in seconds to double check if the master is alive")
    configFlag := flag.String("c", "toggle.conf", "Location of the config file")
	
	flag.Parse()

	if *versionFlag {
		fmt.Printf("\nToggle Version: %s\n\n", API_VER)
		os.Exit(0)
	} else if *intervalFlag < 1 {
        log.Fatalf("Interval time is invalid, must be greater than 0: %d\n", *intervalFlag)
    } else if *retryFlag < 1 {
        log.Fatalf("retry time is invalid, must be greater than 0: %d\n", *intervalFlag)
    }

	loadConfig(&appConfig, *configFlag) //load our config file
    tasks := tasks_c{Config: &appConfig, Retry: *retryFlag} //this "class" handles the actual work, we just need to call it when it's appropriate
    
    //first we want to validate our config so that tasks can run when we schedule it to
    tasks.ValidateConfig()  //if we don't throw a fatal, then we can keep going here

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	
	wg := new(sync.WaitGroup)  //use this to control the exiting of everything
    wg.Add(1)   //add 1 to our group, we can do everything in the background using a single other thread
	
	//tickers for the tasks scheduled at intervals
	ticker := time.NewTicker(time.Second * time.Duration(*intervalFlag))
	
	go func() {
        for range ticker.C {  //every time we "tick"
			writeFlag := tasks.Check()    //main entry point

            if writeFlag {
                fmt.Println("writing new config")
                byt, _ := json.Marshal(appConfig)
                err := ioutil.WriteFile(*configFlag, byt, 0666)
                if err != nil { log.Println(err) }
            }
		}
	}()

	//this stops the api listeners
	go func() {
		<-c
		//for sig := range c {
		// sig is a ^C, handle it
		log.Println("Toggle stopping")

        ticker.Stop()   //this kills the above thread loop
        wg.Done()   //if we're here it's cause the ticker is no more
		//}
	}()
	
	wg.Wait() //wait here until we get an exit ^C request
    log.Println("Toggle Toggle MuthaF*cker")
}
