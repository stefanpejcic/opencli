#!/bin/bash

GREEN='\033[0;32m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m' #reset

echo -e "
Frequently Asked Questions

${PURPLE}1.${NC} What is the login link for admin panel?

execute command ${GREEN}opencli admin${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}2.${NC} How to reset admin password?

execute command ${GREEN}opencli admin password USERNAME NEW_PASSWORD${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}3.${NC} How to create new admin account ?

execute command ${GREEN}opencli admin new USERNAME PASSWORD${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}4.${NC} How to list admin accounts ?

execute command ${GREEN}opencli admin list${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}5.${NC} How to check OpenPanel version ?

execute command ${GREEN}opencli --version${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}6.${NC} How to update OpenPanel ?

execute command ${GREEN}opencli update --force${NC}
${BLUE}------------------------------------------------------------${NC}
${PURPLE}7.${NC} How to disable automatic updates?

execute command ${GREEN}opencli config update autoupdate off${NC}
${BLUE}------------------------------------------------------------${NC}
"
