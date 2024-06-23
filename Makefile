# デフォルトターゲット
install: install_alp install_pt_query_digest install_query_digester

install_alp:
	@echo "Installing alp..."
	cd ~ && \
	wget https://github.com/tkuchiki/alp/releases/download/v1.0.12/alp_linux_amd64.tar.gz && \
	tar xzf alp_linux_amd64.tar.gz && \
	sudo install alp /usr/local/bin/alp && \
	rm alp_linux_amd64.tar.gz

# library2のインストール
install_pt_query_digest:
	@echo "Installing pt_query_digest..."
	# ここにlibrary2をインストールするためのコマンドやスクリプトを記述
	sudo apt install percona-toolkit

install_query_digester:
	@echo "Installing query_digester..."
	cd ~ && \
	git clone https://github.com/kazeburo/query-digester.git && \
	cd query-digester && \
	sudo install query-digester /usr/local/bin


.PHONY: install install_alp install_pt_query_digest install_query_digester 


# ディレクトリのパス
DIGEST_DIR := ./digest-log
# データベースの設定
DB_NAME := isupipe
SQL_DIR := sql/initdb.d

# 最新の .digest ファイルの内容を表示するターゲット
.PHONY: head-latest-digest
head-latest-digest:
	@LINES=$${LINES:-100}; \
	latest_file=$$(ls -t $(DIGEST_DIR)/*.digest 2>/dev/null | head -n 1); \
	if [ -z "$$latest_file" ]; then \
		echo "No .digest file found in $(DIGEST_DIR)."; \
		exit 1; \
	fi; \
	echo "Newest .digest file: $$latest_file"; \
	head -n $$LINES "$$latest_file"


# データベースの再作成とスキーマの初期化
.PHONY: change_db_schema
change_db_schema:
	@echo "Dropping and creating database $(DB_NAME)..."
	@echo "DROP DATABASE IF EXISTS $(DB_NAME);" | sudo mysql
	@echo "CREATE DATABASE $(DB_NAME);" | sudo mysql
	@echo "Initializing database schema..."
	@cat $(SQL_DIR)/10_schema.sql | sudo mysql $(DB_NAME)
	@echo "Database $(DB_NAME) initialized successfully."

