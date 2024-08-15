#!/bin/bash
################################################################################
# Script Name: nginx/install_modsec.sh
# Description: Installs and enables ModSecurity for Nginx.
# Usage: opencli nginx-install_modsec
# Author: Stefan Pejcic
# Created: 22.12.2023
# Last Modified: 22.12.2023
# Company: openpanel.co
# Copyright (c) openpanel.co
# 
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
# 
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
# 
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.
################################################################################

# https://www.faqforge.com/linux/fixed-ubuntu-apt-get-upgrade-auto-restart-services/


sed -i 's/#$nrconf{restart} = '"'"'i'"'"';/$nrconf{restart} = '"'"'a'"'"';/g' /etc/needrestart/needrestart.conf

apt-get update
apt-get install g++ flex bison curl doxygen libyajl-dev libgeoip-dev libtool dh-autoreconf libcurl4-gnutls-dev libxml2 libpcre++-dev libxml2-dev make -y

cd /opt/
git clone https://github.com/SpiderLabs/ModSecurity
cd ModSecurity/
./build.sh
git submodule init
git submodule update


./configure
make ## This step can take 10+ minutes to run!
make install 

apt-get install nginx -y

apt-get install libpcre3 -y
apt-get install libpcre3-dev -y
apt-get install zlib1g -y
apt-get install zlib1g-dev -y
sed -i 's/$nrconf{restart} = '"'"'a'"'"';/#$nrconf{restart} = '"'"'i'"'"';/g' /etc/needrestart/needrestart.conf
apt-get install libssl-dev -y

cd /opt/
git clone --depth 1 https://github.com/SpiderLabs/ModSecurity-nginx.git

wget http://nginx.org/download/nginx-1.18.0.tar.gz
tar zxvf nginx-1.18.0.tar.gz
rm nginx-1.18.0.tar.gz

cd nginx-1.18.0
./configure --with-compat --add-dynamic-module=/opt/ModSecurity-nginx
make modules

cp objs/ngx_http_modsecurity_module.so /usr/share/nginx/modules
cd ~/

sed -i 's/events {/load_module modules\/ngx_http_modsecurity_module.so;\n\nevents {/' /etc/nginx/nginx.conf


mkdir /etc/nginx/modsec
wget -P /etc/nginx/modsec/ https://raw.githubusercontent.com/SpiderLabs/ModSecurity/v3/master/modsecurity.conf-recommended
mv /etc/nginx/modsec/modsecurity.conf-recommended /etc/nginx/modsec/modsecurity.conf

cp /opt/ModSecurity/unicode.mapping /etc/nginx/modsec

sed -i 's/SecRuleEngine DetectionOnly/SecRuleEngine On/' /etc/nginx/modsec/modsecurity.conf


cd ~/
wget https://github.com/coreruleset/coreruleset/archive/refs/tags/v3.3.5.tar.gz

tar -xzvf v3.3.5.tar.gz
rm v3.3.5.tar.gz



sed -i 's/server_name _;/server_name _;\n\tmodsecurity on;\n\tmodsecurity_rules_file \/etc\/nginx\/modsec\/main.conf;/' /etc/nginx/sites-enabled/default


touch /etc/nginx/modsec/main.conf
echo "Include /etc/nginx/modsec/modsecurity.conf" | tee -a /etc/nginx/modsec/main.conf
echo "Include /usr/local/coreruleset-3.3.5/crs-setup.conf" | tee -a /etc/nginx/modsec/main.conf
echo "Include /usr/local/coreruleset-3.3.5/rules/*.conf" | tee -a /etc/nginx/modsec/main.conf

# https://www.faqforge.com/linux/fixed-ubuntu-apt-get-upgrade-auto-restart-services/

mv coreruleset-3.3.5 /usr/local
cp /usr/local/coreruleset-3.3.5/crs-setup.conf.example /usr/local/coreruleset-3.3.5/crs-setup.conf

nginx -s reload
