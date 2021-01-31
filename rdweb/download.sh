#!/bin/bash

# 
# RDWebClientManagement 1.0.3
#
# https://www.powershellgallery.com/packages/RDWebClientManagement/1.0.3

file_url="https://www.powershellgallery.com/api/v2/package/RDWebClientManagement/1.0.3"
file_dir=rdwebclientmanagement.1.0.3
file_name=$file_dir.nupkg


wget -O $file_name  $file_url

if ! md5sum -c ./md5
then
	echo problem with download  $file_name
	exit 1
fi

rm -rf $file_dir
unzip $file_name -d $file_dir


#grep "PackageCatalogUrl = 'https" $file_dir/RDWebClientManagement.psm1
#PackageCatalogUrl='https://go.microsoft.com/fwlink/?linkid=2005418'
eval `awk '/PackageCatalogUrl = .https/ {print "PackageCatalogUrl="$3 }'  $file_dir/RDWebClientManagement.psm1`


wget -O - $PackageCatalogUrl | jq .packages > packages.json
#  "url": "https://query.prod.cms.rt.microsoft.com/cms/api/am/binary/RE4LTEc"
package_url=`jq -r '.[].url' packages.json`
package_ver=`jq -r '.[].version' packages.json`
package_id=`jq -r '.[].packageId' packages.json`

package_file=$package_id.$package_ver.zip
package_dir=$package_id.$package_ver

wget -O $package_file  $package_url 

rm -rf $package_dir 
if ! unzip $package_file -d $package_dir
then 
	echo problem with $package_file 
	exit 1
else
	echo $package_file successfully extracted
fi








