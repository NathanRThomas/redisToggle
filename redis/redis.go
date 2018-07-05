/*! \file redis.go
  \brief Handles any redis connection stuff and calls to it
*/

package redis

import (
	"fmt"
    "time"
	"github.com/mediocregopher/radix.v2/pool"
)

const maxRedisPoolSize = 10      //max number of cache threads waiting in the pool

type Redis_c struct {
	cachePool *pool.Pool
    TestingFlag bool
}


  //-------------------------------------------------------------------------------------------------------------------------//
 //----- SPECIFIC FUNCTIONS ------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

func (r Redis_c) ping () bool {
	rs, _ := r.cachePool.Cmd("PING").Bytes()
    if string(rs[:]) == "PONG" {
        return true
    } else {
        return false
    }
}

func (r Redis_c) set (key, val string) error {
    return r.cachePool.Cmd("SET", key, val).Err
}

//-------------------------------------------------------------------------------------------------------------------------//
//----- INIT FUNCTIONS ----------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

func (r *Redis_c) Connect (ip string, port int) (err error) {
    r.cachePool, err = pool.New("tcp", fmt.Sprintf("%s:%d", ip, port), maxRedisPoolSize)
    if err != nil {
        return fmt.Errorf("Cannont connect to redis server %s:%d :: %s", ip, port, err.Error())
    } else {
        return nil
    }
}

/*! \brief Sets up all the connection pools that we'll need in the future
*/
func (r *Redis_c) Check(masterFlag bool) (error) {
    //do a ping to make sure we got what we expected
    if r.ping() {
        if masterFlag {
            //ping worked, now let's see if we can set things as we expect this to be the master
            if err := r.set("toggle_toggle", time.Now().Format("2006-01-02 15:04:05")); err != nil {
                //if ping works but set doesn't, assume we had a fail over and need to reset this as the master
                fmt.Println("Master unable to be written to, resetting slave of no one")
                return r.Slaveof ("no", "one")  //return this if it errors
            }
        }

        return nil  //we're good
    } else {
        return fmt.Errorf("Unable to 'ping' redis server")
    }
}

/*! \brief Tells the redis server who it should be a slave of
*/
func (r *Redis_c) Slaveof (ip, port string) (error) {
    if r.TestingFlag { return nil } //just testing
    _, err := r.cachePool.Cmd("SLAVEOF", ip, port).Bytes()
    return err
}

func (r *Redis_c) Close () {
    r.cachePool.Empty()
}