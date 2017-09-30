# redisToggle
Used to toggle between two redis machines for better up time

# Concept
Redis handles a simple master replica pretty well, so i was disappointed with current setups that just needed to switch beteween them.
I didn't want to create a 6 server cluster or anything like that, so I created this.

# What this does
This takes a config file, which tells it the ip and ports of two redis servers, a "Master" and a "Slave". Using the stream function of nginx we can create
a reverse proxy for this so that an application can point to the load balancer, and we can update the conf file for nginx to switch between the master
and slave.

# What
Ok, so i use the config file to create a file `/etc/nginx/tcpconf.d/toggle_[port]` which needs to be included in the nginx.conf file like `include /etc/nginx/tcpconf.d/*;`
This app will ping the master server until it fails to connect. At which point it will tell the slave server that it's now the master, update the `toggle_[port]` file to reflect
this, then do an nginx reload.  Which will move the application over to the, still functioning, server that has a pretty close copy of all the cache/redis data. 
It will continue to try to communicate with the old master until it's online again, in which case it will tell it that it's now the slave of the newly switched master.

# Testing
I've tested this over and over, and it seems pretty robust.  The default check interval "-i" is 10 seconds, you can make it quicker if you want, it simply connects and does a 
ping - pong request to the redis server.
