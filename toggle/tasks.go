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
    Port        int     `json:"port"`
}

//app config for what we're monitoring
type appConfig_t  struct {
    Master  server_t  `json:"master"`
    Slave   server_t  `json:"slave"`
}

type tasks_c struct {
    Config  *appConfig_t
    Retry   int
    nginx   nginx.Nginx_c
}

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- PRIVATE FUNCTIONS -------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

func (t *tasks_c) checkRedis (ip string, port int) bool {
    r := redis.Redis_c {}   //init a class
    err := r.Connect(ip, port)
    if err == nil {
        err = r.Check()
    }
    
    if err == nil {
        return true //we can connect
    } else {
        log.Printf("Unable to connect to redis server %s:%d :: %s", ip, port, err.Error())
        return false    //couldn't connect
    }
}

/*! \brief Tells the targer server who their new master is
*/
func (t *tasks_c) slaveof (targetIP string, targetPort int, newMasterIP, newMasterPort string) error {
    r := redis.Redis_c {}   //init a class
    err := r.Connect(targetIP, targetPort)  //connect to the server

    if err == nil {
        err = r.Slaveof(newMasterIP, newMasterPort)   //update the server to let it know who the new master is
    }
    return err
}

/*! \brief The goal here is to keep trying to tell the master that it's no longer the master
    When this fails it ques itself up to try again
*/
func (t *tasks_c) masterToSlave (targetIP string, targetPort int, newMasterIP, newMasterPort string) {
    err := t.slaveof(targetIP, targetPort, newMasterIP, newMasterPort)
    if err != nil { //didn't work
        time.Sleep(time.Second * 5) //sleep here, time is less important as whenever the server comes back online it will start to replicate where it left off
        go t.masterToSlave (targetIP, targetPort, newMasterIP, newMasterPort)   //"recursive call", not actually recursive cause i was worried about a stack overflow
    } else {
        log.Printf("Old master %s converted to slave of %s:%s", targetIP, newMasterIP, newMasterIP) //log that this completed
    }
}

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- PUBLIC FUNCTIONS --------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

/*! \brief Validates the config file.  Call this before you do a Check
    This is intended to be called once at startup, this will validate that we can initially start communicating with all "servers"
    we're expecting to be able to communicate with.  If anything isn't reachable it returns a fatal
*/
func (t *tasks_c) ValidateConfig () {
    if t.checkRedis(t.Config.Master.PublicIP, t.Config.Master.Port) == false { log.Fatalln("Unable to validate Master server") }
    if t.checkRedis(t.Config.Slave.PublicIP, t.Config.Slave.Port) == false { log.Fatalln("Unable to validate Slave server") }

    //if we're here, it's cause things are good, so update the nginx config file to match our config
    t.nginx.Set(t.Config.Master.PublicIP, t.Config.Master.Port)

    //now make sure the servers are correctly identified as master/slave
    t.slaveof(t.Config.Master.PublicIP, t.Config.Master.Port, "no", "one")
    t.slaveof(t.Config.Slave.PublicIP, t.Config.Slave.Port, t.Config.Master.PrivateIP, fmt.Sprintf("%d", t.Config.Master.Port))
    
    log.Println("Config file validated")
}

/*! \brief Main entry point.  Call this and it will check and handle the switch if needed
*/
func (t *tasks_c) Check () (ret bool) {
    if t.checkRedis(t.Config.Master.PublicIP, t.Config.Master.Port) == false { //check the master first
        //if we're here it's cause we couldn't connect with the master redis server
        //we want to make sure we can connect with the slave as well, otherwise there's no point
        if t.checkRedis(t.Config.Slave.PublicIP, t.Config.Slave.Port) {
            //ok, so at this point we couldn't connect to the master, but we could the slave
            //i like to be careful here, so i'm goign to try one more time for the master before we switch everything
            //we passed in a -r flag to indicate the length of time to wait here before we check the master again
            time.Sleep(time.Second * time.Duration(t.Retry))

            if t.checkRedis(t.Config.Master.PublicIP, t.Config.Master.Port) == false {
                //ok, let's switch
                log.Printf("Switching away from old master at %s:%d\n", t.Config.Master.PublicIP, t.Config.Master.Port)
                ret = t.Switch()    //this actually handles switching
            }
        } else {
            log.Println("Lost connection to both master and slave")
        }
    }
    return
}

/*! \brief Handles the process of switching between the slave and master
    essentially 2 things need to happen to make this work
    we need to tell redis that it's now the master, which we'll do first cause it requires connecting to another machine
    and then we need to update the nginx load balancer to switch the reverse proxy to the new slave ip address
    and of course once that's done we want to update our config file to reflect the fact that the master and slave has switched
*/
func (t *tasks_c) Switch () bool {
    err := t.slaveof(t.Config.Slave.PublicIP, t.Config.Slave.Port, "no", "one")   //special no one for indicating it's a master
    if err == nil { //if this worked, then we're committed
        //now update ngnix
        t.nginx.Set(t.Config.Slave.PublicIP, t.Config.Slave.Port)

        //now we need to keep trying to talk to the master server and to let it know it's no longer the master
        t.masterToSlave(t.Config.Master.PublicIP, t.Config.Master.Port, t.Config.Slave.PrivateIP, fmt.Sprintf("%d", t.Config.Slave.Port))

        log.Printf("Switch completed to new master at %s:%d\n", t.Config.Slave.PublicIP, t.Config.Slave.Port)  //we're done
        t.Config.Master, t.Config.Slave = t.Config.Slave, t.Config.Master   //switch the values so we know which is the master and which is the slave now
        return true //indicates we need to write this new update to the config file
    } else {
        log.Printf("Unable to promote slave to master, we're in bad shape: %s \n", err.Error()) //this is really bad
    }

    return false    //this is bad
}

/*! \brief This gets the current settings from the master ip and port for redis
    This will return ip, port, error
*/
func (t *tasks_c) SlaveCheck (ip string, port int) (string, int, error) {
    //we need to do a get request from the master to see what the settings are
    req, err := http.NewRequest("GET", fmt.Sprintf("http://%s:%d", ip, port), nil)
    if err != nil {
        log.Printf("Slave request for master's data failed: %s:%d : %s\n", ip, port, err.Error())
        return "", 0, err
    }
    
    //req.Header.Set("X-Custom-Header", "myvalue")
    req.Header.Set("Accept", "application/json")
    client := &http.Client{}
    
    resp, err := client.Do(req)
    if err != nil {
        log.Printf("Slave request Failed: %s:%d : %s\n", ip, port, err.Error())
        return "", 0, err
    }
    
    defer resp.Body.Close()
    
    //fmt.Println("response Status:", resp.Status)
    //fmt.Println("response Headers:", resp.Header)
    //fmt.Println("response Body:", string(body))
    
    if resp.StatusCode > 299 {
        log.Printf("Slave request Failed code : %d : %s:%d\n", resp.StatusCode, ip, port)
        return "", 0, err
    } else {
        config := appConfig_t{}
        err := json.NewDecoder(resp.Body).Decode(&config)   //unencode the object
        if err == nil {
            return config.Master.PrivateIP, config.Master.Port, nil //we got something
        } else {
            return "", 0, err   //this is bad
        }
    }
}