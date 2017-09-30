/*! \file redis.go
  \brief Handles any redis connection stuff and calls to it
*/

package redis

import (
	"fmt"
	"github.com/mediocregopher/radix.v2/pool"
)

const maxRedisPoolSize = 10      //max number of cache threads waiting in the pool

type Redis_c struct {
	cachePool *pool.Pool
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
func (r *Redis_c) Check() (error) {
    //do a ping to make sure we got what we expected
    if r.ping() {
        return nil  //we're good
    } else {
        return fmt.Errorf("Unable to 'ping' redis server")
    }
}

/*! \brief Tells the redis server who it should be a slave of
*/
func (r *Redis_c) Slaveof (ip, port string) (error) {
    _, err := r.cachePool.Cmd("SLAVEOF", ip, port).Bytes()
    return err
}