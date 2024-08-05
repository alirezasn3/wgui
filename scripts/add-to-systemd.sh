#!/bin/bash

sudo cp ../wgui.service /etc/systemd/system/wgui.service
sudo chmod 664 /etc/systemd/system/wgui.service
sudo systemctl daemon-reload
sudo systemctl start wgui
sudo systemctl enable wgui
sudo systemctl status wgui