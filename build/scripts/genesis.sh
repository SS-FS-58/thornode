#!/bin/sh

. $(dirname "$0")/core.sh

SIGNER_NAME="${SIGNER_NAME:=thorchain}"
SIGNER_PASSWD="${SIGNER_PASSWD:=password}"
NODES="${NODES:=1}"
SEED="${SEED:=thor-daemon}" # the hostname of the master node

# find or generate our BNB address
gen_bnb_address
ADDRESS=$(cat ~/.bond/address.txt)

# create thorchain user
thorcli keys show $SIGNER_NAME || echo $SIGNER_PASSWD | thorcli --trace keys add $SIGNER_NAME 2>&1

VALIDATOR=$(thord tendermint show-validator)
NODE_ADDRESS=$(thorcli keys show thorchain -a)
NODE_PUB_KEY=$(thorcli keys show thorchain -p)
VERSION=$(fetch_version)

if [ "$SEED" = "$(hostname)" ]; then
    echo "I AM THE SEED NODE"
    thord tendermint show-node-id > /tmp/shared/node.txt
fi

# write node account data to json file in shared directory
echo "$NODE_ADDRESS $VALIDATOR $NODE_PUB_KEY $VERSION $ADDRESS" > /tmp/shared/node_$NODE_ADDRESS.json

# enable pools by default
echo "DefaultPoolStatus Enabled $NODE_ADDRESS" > /tmp/shared/config_pool_status.json

# wait until THORNode have the correct number of nodes in our directory before continuing
while [ "$(ls -1 /tmp/shared/node_*.json | wc -l | tr -d '[:space:]')" != "$NODES" ]; do
    sleep 1
done

if [ "$SEED" = "$(hostname)" ]; then
    if [ ! -f ~/.thord/config/genesis.json ]; then
        # get a list of addresses (thor bech32)
        ADDRS=""
        for f in /tmp/shared/node_*.json; do
            ADDRS="$ADDRS,$(cat $f | awk '{print $1}')"
        done
        init_chain $(echo "$ADDRS" | sed -e 's/^,*//')

        if [ ! -z ${VAULT_PUBKEY+x} ]; then
            PUBKEYS=""
            for f in /tmp/shared/node_*.json; do
                PUBKEYS="$PUBKEYS,$(cat $f | awk '{print $3}')"
            done
            add_vault $VAULT_PUBKEY $(echo "$PUBKEYS" | sed -e 's/^,*//')
        fi

        # add node accounts to genesis file
        for f in /tmp/shared/node_*.json; do 
            if [ ! -z ${VAULT_PUBKEY+x} ]; then
                add_node_account $(cat $f | awk '{print $1}') $(cat $f | awk '{print $2}') $(cat $f | awk '{print $3}') $(cat $f | awk '{print $4}') $(cat $f | awk '{print $5}') $VAULT_PUBKEY
            else
                add_node_account $(cat $f | awk '{print $1}') $(cat $f | awk '{print $2}') $(cat $f | awk '{print $3}') $(cat $f | awk '{print $4}') $(cat $f | awk '{print $5}')
            fi
        done

        for f in /tmp/shared/config_*.json; do
          add_admin_config $(cat $f | awk '{print $1}') $(cat $f | awk '{print $2}') $(cat $f | awk '{print $3}')
        done

        cat ~/.thord/config/genesis.json
        thord validate-genesis
    fi
fi

# setup peer connection
if [ "$SEED" != "$(hostname)" ]; then
    if [ ! -f ~/.thord/config/genesis.json ]; then
        echo "I AM NOT THE SEED"
        
        init_chain $NODE_ADDRESS
        fetch_genesis $SEED
        NODE_ID=$(fetch_node_id $SEED)
        echo "NODE ID: $NODE_ID"
        peer_list $NODE_ID $SEED

        cat ~/.thord/config/genesis.json
    fi
fi


exec "$@"
