#!/bin/bash

# エラーが発生したらスクリプトを中断
set -e

# 実行するコマンドを出力
set -x


current_branch=$(git branch --show-current)
echo "現在のブランチ: $current_branch"

# Gitのmasterブランチをpull
echo "Pulling the latest code from the master branch..."
git pull origin main

# MySQL設定ファイルをコピー
echo "Copying MySQL configuration..."
# sudo cp etc/mysql/my.cnf /etc/mysql/my.cnf
sudo cp etc/mysql/mysql.conf.d/mysqld.cnf /etc/mysql/mysql.conf.d/mysqld.cnf

# Nginx設定ファイルをコピー
echo "Copying Nginx configuration..."
sudo cp etc/nginx/nginx.conf /etc/nginx/
# sudo cp etc/nginx/conf.d/default.conf /etc/nginx/conf.d/
# sudo cp etc/nginx/sites-available/isucholar.conf /etc/nginx/sites-available/

# Golangディレクトリに移動
echo "Changing directory to golang..."
cd go

# Goビルド
echo "Building the Go application..."
go build -o isupipe

# MySQLとNginxのログを初期化
echo "Resetting MySQL and Nginx logs..."
sudo truncate -s 0 /var/log/mysql/slow-query.log
sudo truncate -s 0 /var/log/nginx/access.log

# log permission
sudo chmod 777 /var/log/nginx /var/log/nginx/*
sudo chmod 777 /var/log/mysql /var/log/mysql/*
# isu-go, mysql, nginxを再起動
echo "Restarting isu-go, mysql, nginx..."
sudo systemctl restart isupipe-go.service 
sudo systemctl restart mysql
sudo systemctl restart nginx

# MySQLのスロークエリログを有効化
echo "Enabling MySQL slow query log..."
# sudo mysql -u root -e "SET GLOBAL slow_query_log = 'ON';"
# sudo mysql -u root -e "SET GLOBAL long_query_time = 0.0;"
# sudo mysql -u root -e "SET GLOBAL slow_query_log_file = '/var/log/mysql/slow-query.log';"

echo "All tasks completed successfully."