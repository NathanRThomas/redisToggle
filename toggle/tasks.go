/*! \file tasks.go
    \brief This actually handles the background tasks and what they entail
*/

package main

import (
    "fmt"
    "log"
    "time"
    "encoding/json"
    "net/http"

    "github.com/NathanRThomas/redisToggle/redis"
    "github.com/NathanRThomas/redisToggle/nginx"
)

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- STRUCT ------------------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

type server_t struct {
    PublicIP    string  `json:"public_ip"`
    PrivateIP   string  `json:"private_ip"`
}

//app config for what we're monitoring
type appConfig_t  struct {
    Main  server_t  `json:"main"`
    Subordinate   server_t  `json:"subordinate"`
    Ports   []int     `json:"ports"`
}

type tasks_c struct {
    Config  *appConfig_t
    Retry   int
    TestingFlag bool
    nginx   nginx.Nginx_c
}

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- PRIVATE FUNCTIONS -------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

func (t *tasks_c) checkRedis (ip string, port int, mainFlag bool) bool {
    r := redis.Redis_c { TestingFlag: t.TestingFlag }   //init a class
    err := r.Connect(ip, port)
    if err == nil {
        defer r.Close()
        err = r.Check(mainFlag)
    }
    
    if err == nil {
        return true //we can connect
    } else {
        log.Printf("Unable to connect to redis server %s:%d :: %s", ip, port, err.Error())
        return false    //couldn't connect
    }
}

/*! \brief Tells the targer server who their new main is
*/
func (t *tasks_c) subordinateof (targetIP string, targetPort int, newMainIP, newMainPort string) error {
    r := redis.Redis_c { TestingFlag: t.TestingFlag }   //init a class
    err := r.Connect(targetIP, targetPort)  //connect to the server

    if err == nil {
        defer r.Close()
        err = r.Subordinateof(newMainIP, newMainPort)   //update the server to let it know who the new main is
    }
    return err
}

/*! \brief The goal here is to keep trying to tell the main that it's no longer the main
    When this fails it ques itself up to try again
*/
func (t *tasks_c) mainToSubordinate (targetIP, newMainIP string, targetPort int) {
    err := t.subordinateof(targetIP, targetPort, newMainIP, fmt.Sprintf("%d", targetPort))
    if err != nil { //didn't work
        time.Sleep(time.Second * 5) //sleep here, time is less important as whenever the server comes back online it will start to replicate where it left off
        go t.mainToSubordinate (targetIP, newMainIP, targetPort)   //"recursive call", not actually recursive cause i was worried about a stack overflow
    } else {
        log.Printf("Old main %s converted to subordinate of %s:%s", targetIP, newMainIP, newMainIP) //log that this completed
    }
}

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- PUBLIC FUNCTIONS --------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

/*! \brief Validates the config file.  Call this before you do a Check
    This is intended to be called once at startup, this will validate that we can initially start communicating with at least the main server
    We don't specifically care if we can't connect to the subordinate, although that is bad, we don't want that to prevent us from starting this service 
    on account of a bad subordinate connection
*/
func (t *tasks_c) ValidateConfig () {
    t.nginx.TestingFlag = t.TestingFlag //pass this down

    allGood := true     //default to this
    for _, port := range t.Config.Ports {
        if t.checkRedis(t.Config.Main.PublicIP, port, true) == false {  //see if we can connect to the main
            allGood = false
            break   //we couldn't connect to one of the main ports
        }
    }

    if !allGood {   //this didn't work, so now try to connect to the subordinate instead
        for _, port := range t.Config.Ports {
            if t.checkRedis(t.Config.Subordinate.PublicIP, port, false) == false {  //see if we can connect to the subordinate
                //this is really bad, we couldn't successfully connect to the main or the subordinate, so we have to bail
                log.Fatalf("Unable to connect to main or subordinate on port %d\n", port)
            }
        }
    }

    if !allGood {   //in this case we couldn't talk to the main, but we could talk to the subordinate, so we want to switch them
        if !t.Switch() {
            log.Fatalln("We were not able to convert the subordinate over to a main")
        }
    } else {
        //if we're here, it's cuase things are good, so update the nginx config file to match our config
        t.nginx.Set(t.Config.Main.PublicIP, t.Config.Ports)

        //now make sure the servers are correctly identified as main/subordinate
        for _, port := range t.Config.Ports {
            t.subordinateof(t.Config.Main.PublicIP, port, "no", "one")
            t.subordinateof(t.Config.Subordinate.PublicIP, port, t.Config.Main.PrivateIP, fmt.Sprintf("%d", port))
        }
    }
    log.Println("Config file validated")
}

/*! \brief Main entry point.  Call this and it will check and handle the switch if needed
*/
func (t *tasks_c) Check () (ret bool) {
    for _, port := range t.Config.Ports {
        if t.checkRedis(t.Config.Main.PublicIP, port, true) == false { //check the main first
            //if we're here it's cause we couldn't connect with the main redis server
            //we want to make sure we can connect with the subordinate as well, otherwise there's no point
            if t.checkRedis(t.Config.Subordinate.PublicIP, port, false) {
                //ok, so at this point we couldn't connect to the main, but we could the subordinate
                //i like to be careful here, so i'm goign to try one more time for the main before we switch everything
                //we passed in a -r flag to indicate the length of time to wait here before we check the main again
                time.Sleep(time.Second * time.Duration(t.Retry))

                if t.checkRedis(t.Config.Main.PublicIP, port, true) == false {
                    //ok, let's switch
                    log.Printf("Switching away from old main at %s:%d\n", t.Config.Main.PublicIP, port)
                    ret = t.Switch()    //this actually handles switching
                }
            } else {
                log.Println("Lost connection to both main and subordinate")
            }
        }
    }
    return
}

/*! \brief Handles the process of switching between the subordinate and main
    essentially 2 things need to happen to make this work
    we need to tell redis that it's now the main, which we'll do first cause it requires connecting to another machine
    and then we need to update the nginx load balancer to switch the reverse proxy to the new subordinate ip address
    and of course once that's done we want to update our config file to reflect the fact that the main and subordinate has switched
*/
func (t *tasks_c) Switch () bool {
    var err error
    for _, port := range t.Config.Ports {
        err = t.subordinateof(t.Config.Subordinate.PublicIP, port, "no", "one")   //special no one for indicating it's a main
        if err == nil {
            //now we need to keep trying to talk to the main server and to let it know it's no longer the main
            t.mainToSubordinate(t.Config.Main.PublicIP, t.Config.Subordinate.PrivateIP, port)
        } else {
            break   //don't do anymore, we're done
        }
    }

    if err == nil { //if this worked, then we're committed
        //now update ngnix
        t.nginx.Set(t.Config.Subordinate.PublicIP, t.Config.Ports)

        log.Printf("Switch completed to new main at %s\n", t.Config.Subordinate.PublicIP)  //we're done
        t.Config.Main, t.Config.Subordinate = t.Config.Subordinate, t.Config.Main   //switch the values so we know which is the main and which is the subordinate now
        return true //indicates we need to write this new update to the config file
    } else {
        log.Printf("Unable to promote subordinate to main, we're in bad shape: %s \n", err.Error()) //this is really bad
    }
    return false    //this is bad
}

/*! \brief This gets the current settings from the main ip and port for redis
    This will return ip, port, error
*/
func (t *tasks_c) SubordinateCheck (ip string, port int) (config appConfig_t, err error) {
    //we need to do a get request from the main to see what the settings are
    req, err := http.NewRequest("GET", fmt.Sprintf("http://%s:%d", ip, port), nil)
    if err != nil {
        log.Printf("Subordinate request for main's data failed: %s:%d : %s\n", ip, port, err.Error())
        return
    }
    
    //req.Header.Set("X-Custom-Header", "myvalue")
    req.Header.Set("Accept", "application/json")
    client := &http.Client{}
    
    resp, err := client.Do(req)
    if err != nil {
        log.Printf("Subordinate request Failed: %s:%d : %s\n", ip, port, err.Error())
        return
    }
    
    defer resp.Body.Close()
    
    //fmt.Println("response Status:", resp.Status)
    //fmt.Println("response Headers:", resp.Header)
    //fmt.Println("response Body:", string(body))
    
    if resp.StatusCode > 299 {
        err = fmt.Errorf("Subordinate request Failed code : %d : %s:%d\n", resp.StatusCode, ip, port)
    } else {
        err = json.NewDecoder(resp.Body).Decode(&config)   //unencode the object
    }
    return  //for better or worse, we're done
}