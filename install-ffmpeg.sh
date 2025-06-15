#!/bin/bash
# Install ffmpeg on the server
echo "Installing ffmpeg..."
sudo apt update
sudo apt install -y ffmpeg
echo "ffmpeg installation completed"
ffmpeg -version