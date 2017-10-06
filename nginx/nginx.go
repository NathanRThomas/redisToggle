/*! \file nginx.go
    \brief This is used to update/create the nginx config file for handling reverse proxies for redis
*/

package nginx

import (
    "fmt"
    "log"
    "os"
    "os/exec"
    "io/ioutil"
)

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- CONST -------------------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

const nginx_dir     = "/etc/nginx"
const nginx_tcp_dir = "tcpconf.d"
const conf_file     = "toggle_"

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- STRUCT ------------------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

type Nginx_c struct {

}

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- PRIVATE FUNCTIONS -------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

/*! \brief This generates a string that represents the config file for nginx to pass requests to the upstream ip and port
*/
func (n *Nginx_c) genStream (ip string, port int) ([]byte) {
    return []byte(fmt.Sprintf("\n\tupstream redis_%d {\n\t\tserver %s:%d;\n\t}\n\n\tserver {\n\t\tlisten %d;\n\t\tproxy_pass redis_%d;\n\t}\n\n",
        port, ip, port, port, port))
}

func (n *Nginx_c) reload() {
    _, err := exec.LookPath("nginx")
    if err == nil { //nginx is installed, so go with it
        cmd := exec.Command("systemctl", "reload", "nginx")
        err  = cmd.Run()
        if err != nil {
            log.Printf("Unable to reload nginx: %s", err.Error())
        }
    } else {
        log.Fatalln("Nginx does not appear to be installed.  Toggle requires nginx")
    }
}

  //-------------------------------------------------------------------------------------------------------------------------//
 //----- PUBLIC FUNCTIONS --------------------------------------------------------------------------------------------------//
//-------------------------------------------------------------------------------------------------------------------------//

/*! \brief Main entry point, this handles setting of the nginx config file and ensuring it's enabled and nginx has it reloaded
*/
func (n *Nginx_c) Set (ip string, port int) error {

    err := os.MkdirAll(fmt.Sprintf("%s/%s", nginx_dir, nginx_tcp_dir), 0755)   //create the directory to store the config file in

    if err == nil { //we have a dir, now let's dump to file
        fileName := fmt.Sprintf("%s/%s/%s%d", nginx_dir, nginx_tcp_dir, conf_file, port)
        err := ioutil.WriteFile(fileName, n.genStream(ip, port), 0644)

        if err == nil { //we wrote the config file
            n.reload()//we need to get nginx to reload
        }
    }

    return nil
}