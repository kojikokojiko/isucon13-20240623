# デフォルトターゲット
all: install_alp install_pt_query_digest install_query_digester

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


# ディレクトリのパス
DIGEST_DIR := ./digest-log

# 最新の .digest ファイルの内容を表示するターゲット
head-latest-digest:
	@LINES=$${LINES:-10}; \
	latest_file=$$(ls -t $(DIGEST_DIR)/*.digest 2>/dev/null | head -n 1); \
	if [ -z "$$latest_file" ]; then \
		echo "No .digest file found in $(DIGEST_DIR)."; \
		exit 1; \
	fi; \
	echo "Newest .digest file: $$latest_file"; \
	head -n $$LINES "$$latest_file"


.PHONY: all install_alp install_pt_query_digest install_query_digester head-latest-digest