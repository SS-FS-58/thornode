require_relative './helper.rb'

TRUST_BNB_ADDRESS = "tbnb1tdfqy34uptx207scymqsy4k5uzfmry5s8lujqt"

describe "API Tests" do

  context "Check /ping responds" do
    it "should return 'pong'" do
      resp = get("/ping")
      expect(resp.code).to eq("200")
      expect(resp.body['ping']).to eq "pong"
    end
  end

  context "Check that an empty tx hash returns properly" do
    it "should have no values" do
      resp = get("/tx/A9A65505553D777E5CE957A74153F21EDD8AAA4B0868F2537E97E309945425B9")
      expect(resp.body['tx']['memo']).to eq(""), resp.body.inspect
      expect(resp.body['status']).to eq(""), resp.body.inspect
      expect(resp.body['out_hashes']).to eq(nil), resp.body.inspect
    end
  end

  context "Check THORNode have no completed events" do
    it "should be a nil" do
      resp = get("/events/1")
      expect(resp.body).to eq([]), resp.body.inspect
    end
  end


  context "Create a pool" do

    it "should show up in listing of pools" do
      resp = get("/pools")
      # Previously THORNode add BNB pool in genesis , but now THORNode removed it
      expect(resp.body).to eq([]), "Are you working from a clean blockchain? Did you wait until 1 block was create? \n(#{resp.code}: #{resp.body})"
    end

  end

  context "Add gas" do
    it "adds gas" do
      coins = [
        {'asset': 'BNB.BNB', "amount": "20000000"},
      ]
      tx = makeTx(memo: "GAS", coins: coins)
      resp = processTx(tx)
      expect(resp.code).to eq("200"), resp.body.inspect
    end
  end

  context "Show supporting chains" do
    it "should have BNB in the list of support chains" do
      resp = get("/chains")
      expect(resp.body[0]).to eq("BNB"), resp.body.inspect
    end
  end

  sender = "bnb1xlvns0n2mxh77mzaspn2hgav4rr4m8eerfju38"
  context "Stake/Unstake" do

    coins = [
      {'asset': 'BNB.RUNE-B1A', "amount": "2349500000"},
      {'asset': 'BNB.TCAN-014', "amount": "334850000"},
    ]

    it "should be able to stake" do

      tx = makeTx(memo: "stake:TCAN-014", coins: coins, sender: sender)
      resp = processTx(tx)
      expect(resp.code).to eq("200"), resp.body.inspect

      resp = get("/pool/TCAN-014/stakers")
      expect(resp.code).to eq("200"), resp.body.inspect
      expect(resp.body['stakers'].length).to eq(1), resp.body['stakers'].inspect
      expect(resp.body['stakers'][0]['units']).to eq("1342175000"), resp.body['stakers'][0].inspect
    end

    it "check for stake event" do
      resp = get("/events/2")
      expect(resp.body[0]['id']).to eq("2"), resp.body[0].inspect
      expect(resp.body[0]['type']).to eq("stake"), resp.body[0].inspect
    end

    it "should be able to unstake" do
      tx = makeTx(memo: "withdraw:TCAN-014", sender: sender)
      resp = processTx(tx)
      expect(resp.code).to eq("200"), resp.body.inspect

      resp = get("/pool/BNB.TCAN-014/stakers")
      expect(resp.code).to eq("200"), resp.body.inspect
      expect(resp.body['stakers']).to eq(nil), resp.body.inspect
    end

    it "check for unstake event trigger pool event" do # check unstaking last staker creates pool event
      resp = get("/events/3")
      expect(resp.body.count).to eq(1), resp.body.inspect
      expect(resp.body[0]['id']).to eq("3"), resp.body[0].inspect
      expect(resp.body[0]['type']).to eq("pool"), resp.body[0].inspect
      expect(resp.body[0]['event']['status']).to eq("Bootstrap"), resp.body[0].inspect
    end

  end

  context "Swap" do
    txid = txid() # outside it state so its value is available in multiple "it" statements
    it "swap" do
      coins = [
        {'asset': 'BNB.RUNE-B1A', "amount": "2349500000"},
        {'asset': 'BNB.BOLT-014', "amount": "334850000"},
      ]
      # stake some coins first
      tx = makeTx(memo: "stake:BNB.BOLT-014", coins: coins, sender: sender)
      resp = processTx(tx)
      expect(resp.code).to eq("200"), resp.body.inspect
      resp = get("/pool/BOLT-014")
      expect(resp.code).to eq("200")
      expect(resp.body['balance_rune']).to eq("2349500000"), resp.body.inspect
      expect(resp.body['balance_asset']).to eq("334850000"), resp.body.inspect

      # make a swap
      coins = [
        {'asset': 'BNB.BOLT-014', "amount": "20000000"},
      ]
      tx = makeTx(
        memo: "swap:RUNE-B1A:bnb1ntqj0v0sv62ut0ehxt7jqh7lenfrd3hmfws0aq:124958592",
        coins: coins,
        hash: txid,
      )
      resp = processTx(tx)
      expect(resp.code).to eq("200"), resp.body.inspect

      resp = get("/pool/BOLT-014")
      expect(resp.code).to eq("200")
      expect(resp.body['balance_rune']).to eq("2217077851"), resp.body.inspect
      expect(resp.body['balance_asset']).to eq("354850000"), resp.body.inspect

      # another swap ,it should fail due to price protection
      tx1 = makeTx(
        memo: "swap:RUNE-B1A:bnb1ntqj0v0sv62ut0ehxt7jqh7lenfrd3hmfws0aq:134958590000000",
        coins: coins,
        hash: txid(),
      )
      resp = processTx(tx1)
      expect(resp.code).to eq("200"), resp.body.inspect

      # pool balance should not change (other than the transaction fees)
      resp = get("/pool/BNB.BOLT-014")
      expect(resp.code).to eq("200")
      expect(resp.body['balance_rune']).to eq("2117077851"), resp.body.inspect
      expect(resp.body['balance_asset']).to eq("370855302"), resp.body.inspect
    end

    it "Send outbound tx and mark tx'es as complete" do
      # find the block height of the previous swap transaction
      i = 1
      found = false
      until i > 40
        resp = get("/keysign/#{i}")
        if not resp.body['chains'].include?("BNB")
          i = i + 1
          next
        end
        arr = resp.body['chains']['BNB']
        unless arr['tx_array'].empty?
          for idx in 0 ...arr['tx_array'].size
            # THORNode have found the block height of our last swap
            newTxId = txid()
            coin = arr['tx_array'][idx]['coin']
            coins = [{
              'asset': coin['asset'],
              'amount': coin['amount'],
            }]
            toAddr = arr['tx_array'][idx]['to']
            tx = makeTx(memo: arr['tx_array'][idx]['memo'], hash:newTxId, coins:coins , sender:toAddr, outbound:true)
            resp = processTx(tx)
            expect(resp.code).to eq("200"), resp.body.inspect
          end
          if arr['tx_array'][idx]['to'] == "bnb1ntqj0v0sv62ut0ehxt7jqh7lenfrd3hmfws0aq"
            found = true
            resp = get("/tx/#{txid}")
            expect(resp.code).to eq("200")
            expect(resp.body['out_hashes']).to eq([newTxId]), resp.body.inspect
            break
          end
        end
        i = i + 1
      end

      expect(found).to eq(true)

    end

    it "check events are completed" do
      resp = get("/events/6")
      expect(resp.body.count).to eq(1), resp.body.inspect
      expect(resp.body[0]['event']['pool']).to eq("BNB.BOLT-014"), resp.body[0].inspect
      expect(resp.body[0]['type']).to eq("swap"), resp.body[0].inspect
      expect(resp.body[0]['in_tx']['id']).to eq(txid), resp.body[0].inspect
      expect(resp.body[0]['out_txs'][0]['id'].length).to eq(64), resp.body[0].inspect
    end

    it "add assets to a pool" do
      coins = [
        {'asset': 'BNB.RUNE-B1A', "amount": "20000000"},
        {'asset': 'BNB.BOLT-014', "amount": "20000000"},
      ]
      tx = makeTx(memo: "add:BOLT-014", coins: coins, sender: sender)
      resp = processTx(tx)
      expect(resp.code).to eq("200"), resp.body.inspect

      resp = get("/pool/BOLT-014")
      expect(resp.code).to eq("200")
      expect(resp.body['balance_rune']).to eq("2144541407"), resp.body.inspect
      expect(resp.body['balance_asset']).to eq("390801602"), resp.body.inspect
    end

  end

  context "Block heights" do
    it "ensure THORNode have non-zero block height" do
      resp = get("/lastblock")
      expect(resp.code).to eq("200")
      expect(resp.body['chain']).to eq("BNB"), resp.body.inspect
      expect(resp.body['lastobservedin']).to eq("376"), resp.body.inspect
      expect(resp.body['lastsignedout'].to_i).to be > 0, resp.body.inspect
      expect(resp.body['statechain'].to_i).to be > 1, resp.body.inspect

      resp = get("/lastblock/bnb")
      expect(resp.code).to eq("200")
      expect(resp.body['chain']).to eq("BNB"), resp.body.inspect
      expect(resp.body['lastobservedin']).to eq("376"), resp.body.inspect
      expect(resp.body['lastsignedout'].to_i).to be > 0, resp.body.inspect
      expect(resp.body['statechain'].to_i).to be > 1, resp.body.inspect
    end
  end

end
