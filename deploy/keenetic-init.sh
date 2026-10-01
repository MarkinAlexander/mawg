#!/bin/sh

case "$1" in
  start)
    start-stop-daemon -S -b -m -p /opt/var/run/mawg.pid -x /opt/bin/mawg -- -base /opt/etc/mawg
    ;;
  stop)
    start-stop-daemon -K -p /opt/var/run/mawg.pid -x /opt/bin/mawg
    ;;
  restart)
    "$0" stop
    sleep 1
    "$0" start
    ;;
  status)
    if [ -f /opt/var/run/mawg.pid ] && kill -0 "$(cat /opt/var/run/mawg.pid)" 2>/dev/null; then
      echo running
    else
      echo stopped
    fi
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|status}"
    ;;
esac
