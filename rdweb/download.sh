#!/bin/bash

# 
# RDWebClientManagement 1.0.3
#
# https://www.powershellgallery.com/packages/RDWebClientManagement/1.0.3

file_url="https://www.powershellgallery.com/api/v2/package/RDWebClientManagement/1.0.3"
file_dir=rdwebclientmanagement.1.0.3
file_name=$file_dir.nupkg


echo wget -O $file_name  $file_url

unzip $file_name -d $file_dir

md5sum -c ./md5


grep "PackageCatalogUrl = 'https" $file_dir/RDWebClientManagement.psm1

PackageCatalogUrl='https://go.microsoft.com/fwlink/?linkid=2005418'

wget -O - $PackageCatalogUrl | jq .packages > packages.json
#  "url": "https://query.prod.cms.rt.microsoft.com/cms/api/am/binary/RE4LTEc"
package_url=`jq -r '.[].url' packages.json`
package_ver=`jq -r '.[].version' packages.json`
package_id=`jq -r '.[].packageId' packages.json`

package_file=$package_id.$package_ver.zip

wget -O $package_file  $package_url

unzip $package_file








