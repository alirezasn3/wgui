#!/bin/bash

cd
curl -OL https://golang.org/dl/go1.21.6.linux-amd64.tar.gz
sudo tar -C /usr/local -xvf go1.21.6.linux-amd64.tar.gz
rm go1.21.6.linux-amd64.tar.gz
echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.profile
echo "Run this command to complete installation: source ~/.profile"