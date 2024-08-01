package uploader

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	walletTypes "github.com/desmos-labs/cosmos-go-wallet/types"
	"github.com/desmos-labs/cosmos-go-wallet/wallet"
	canine "github.com/jackalLabs/canine-chain/v3/app"
	"github.com/jackalLabs/canine-chain/v3/x/storage/types"
	"github.com/jackalLabs/canine-chain/v3/x/storage/utils"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

var blackList = make(map[string]bool)

type ErrorResponse struct {
	Error string `json:"error"`
}

type IPFSResponse struct {
	Cid string `json:"cid"`
}

func isJSONResponse(url string) (bool, error) {
	// Make the HTTP request
	cl := http.DefaultClient
	//cl.Timeout = time.Second * 2
	resp, err := cl.Get(url)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	// Try to parse the response body as JSON
	var js map[string]string
	if err := json.Unmarshal(body, &js); err != nil {
		return false, fmt.Errorf("response body is not valid JSON: %s", err)
	}

	if js["chain-id"] == "jackal-1" {
		return true, nil
	}

	return false, nil
}

func uploadFile(ip string, r io.Reader, merkle []byte, start int64, address string) (string, error) {
	cli := http.DefaultClient
	//cli.Timeout = time.Second * 5
	u, err := url.Parse(ip)
	if err != nil {
		return "", err
	}

	path := u.JoinPath("version").String()

	fmt.Println(path)
	isJson, err := isJSONResponse(path)
	if err != nil {
		return "", fmt.Errorf("response is not json | %w", err)
	}
	if !isJson {
		return "", fmt.Errorf("version is not valid json")
	}

	u = u.JoinPath("upload")

	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	defer writer.Close()

	err = writer.WriteField("sender", address)
	if err != nil {
		return "", err
	}

	err = writer.WriteField("merkle", hex.EncodeToString(merkle))
	if err != nil {
		return "", err
	}

	err = writer.WriteField("start", fmt.Sprintf("%d", start))
	if err != nil {
		return "", err
	}

	fileWriter, err := writer.CreateFormFile("file", hex.EncodeToString(merkle))
	if err != nil {
		return "", err
	}

	_, err = io.Copy(fileWriter, r)
	if err != nil {
		return "", err
	}
	writer.Close()

	req, _ := http.NewRequest("POST", u.String(), &b)
	req.Header.Add("Content-Type", writer.FormDataContentType())

	res, err := cli.Do(req)
	if err != nil {
		return "", err
	}

	defer res.Body.Close()

	if res.StatusCode != 200 {

		var errRes ErrorResponse

		err := json.NewDecoder(res.Body).Decode(&errRes)
		if err != nil {
			return "", err
		}

		return "", fmt.Errorf("upload failed with code %d | %s", res.StatusCode, errRes.Error)
	}

	var ipfsRes IPFSResponse
	err = json.NewDecoder(res.Body).Decode(&ipfsRes)
	if err != nil {
		return "", err
	}

	return ipfsRes.Cid, nil
}

func Post(msg sdk.Msg, w *wallet.Wallet) (*sdk.TxResponse, error) {
	fmt.Println("posting message...")

	data := walletTypes.NewTransactionData(
		msg,
	).WithGasAuto().WithFeeAuto()

	res, err := w.BroadcastTxCommit(data)

	return res, err
}

func PostWithFee(msg sdk.Msg, w *wallet.Wallet) (*sdk.TxResponse, error) {
	fmt.Println("posting message...")

	data := walletTypes.NewTransactionData(
		msg,
	).WithGasLimit(100_000).WithFeeAmount(sdk.NewCoins(sdk.NewInt64Coin("ujkl", 200)))

	res, err := w.BroadcastTxCommit(data)

	return res, err
}

func PostFile(fileData []byte, w *wallet.Wallet) (string, []byte, error) {
	buf := bytes.NewBuffer(fileData)
	treeBuffer := bytes.NewBuffer(buf.Bytes())

	abci, err := w.Client.RPCClient.ABCIInfo(context.Background())
	if err != nil {
		return "", nil, err
	}

	cl := types.NewQueryClient(w.Client.GRPCConn)

	params, err := cl.Params(context.Background(), &types.QueryParams{})
	if err != nil {
		return "", nil, err
	}

	root, _, _, size, err := utils.BuildTree(treeBuffer, params.Params.ChunkSize)
	if err != nil {
		return "", root, err
	}

	address := w.AccAddress()

	msg := types.NewMsgPostFile(
		address,
		root,
		int64(size),
		40,
		0,
		3,
		"{\"memo\":\"Uploaded with Jackal Bot\"}",
	)
	msg.Expires = abci.Response.LastBlockHeight + ((100 * 365 * 24 * 60 * 60) / 6)
	if err := msg.ValidateBasic(); err != nil {
		return "", root, err
	}

	res, err := Post(msg, w)
	if err != nil {
		return "", root, err
	}
	fmt.Println("finished waiting for queue...")
	if res == nil {
		return "", root, fmt.Errorf("response is empty")
	}
	if res.Code != 0 {
		return "", root, fmt.Errorf(res.RawLog)
	}

	var postRes types.MsgPostFileResponse
	resData, err := hex.DecodeString(res.Data)
	if err != nil {
		return "", root, err
	}

	encodingCfg := canine.MakeEncodingConfig()
	var txMsgData sdk.TxMsgData
	err = encodingCfg.Marshaler.Unmarshal(resData, &txMsgData)
	if err != nil {
		return "", root, err
	}

	fmt.Println(txMsgData)
	if len(txMsgData.Data) == 0 {
		return "", root, fmt.Errorf("no message data")
	}

	err = postRes.Unmarshal(txMsgData.Data[0].Data)
	if err != nil {
		return "", root, err
	}

	fmt.Println(res.Code)
	fmt.Println(res.RawLog)
	fmt.Println(res.TxHash)

	c := ""

	pageReq := &query.PageRequest{
		Key:        nil,
		Offset:     0,
		Limit:      500,
		CountTotal: true,
		Reverse:    true,
	}
	provReq := types.QueryAllProviders{
		Pagination: pageReq,
	}

	provRes, err := cl.AllProviders(context.Background(), &provReq)
	if err != nil {
		return c, root, err
	}

	providers := provRes.Providers

	for i := range providers {
		j := rand.Intn(i + 1)
		providers[i], providers[j] = providers[j], providers[i]
	}

	var i int64

	for _, provider := range providers {
		if i >= 3 {
			break
		}
		if blackList[provider.Address] {
			fmt.Printf("Skipping %s\n", provider.Ip)

			continue
		}
		fmt.Println(provider.Ip)
		uploadBuffer := bytes.NewBuffer(buf.Bytes())
		cid, err := uploadFile(provider.Ip, uploadBuffer, root, postRes.StartBlock, address)
		if err != nil {
			fmt.Println(err)
			if strings.Contains(err.Error(), "cannot accept file that I cannot claim") {
				continue
			}
			blackList[provider.Address] = true
			continue
		}
		if len(c) == 0 {
			c = cid
		}
		i++
	}
	return c, root, nil
}
