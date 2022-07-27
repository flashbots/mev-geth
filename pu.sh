SERVER_NAME=${1}

rsync -rav --exclude ".git" --exclude "test" \
      . \
      ethos@$SERVER_NAME:~/mev_geth_node/mev-geth
