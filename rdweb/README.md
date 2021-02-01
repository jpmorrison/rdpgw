## Download and extract current MS RD web client without powershell

Dependencies: wget, awk, jq, unzip 

Usage:

./download.sh

If the download script breaks,  the URLs saved in [allpackages.json](https://raw.githubusercontent.com/jpmorrison/rdpgw/master/rdweb/allpackages.json) *may* still work.

TODO: test this with rdpgw

https://docs.microsoft.com/en-us/windows-server/remote/remote-desktop-services/clients/remote-desktop-web-client-admin
https://www.powershellgallery.com/packages/RDWebClientManagement/1.0.3
