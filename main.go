package main

import (
	"context"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/go-bip39"
	"github.com/desmos-labs/cosmos-go-wallet/types"
	"github.com/desmos-labs/cosmos-go-wallet/wallet"
	"github.com/dgraph-io/badger/v2"
	"github.com/joho/godotenv"
	"io"
	"log"
	"net/http"
	url2 "net/url"
	"os"
	"tgbot/jackal/uploader"
	jWallet "tgbot/jackal/wallet"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var userUploads = make(map[int64]int)

var RPC = "https://rpc.jackalprotocol.com:443"
var GRPC = "jackal-grpc.polkachu.com:17590"

func HandlePhoto(bot *tgbotapi.BotAPI, msgToDelete int, w *wallet.Wallet, message *tgbotapi.Message) {
	delMsg := tgbotapi.NewDeleteMessage(message.Chat.ID, msgToDelete)
	_, err := bot.Send(delMsg)
	if err != nil {
		fmt.Println(err)
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, "Uploading your file to Jackal...")
	msg.ReplyToMessageID = message.MessageID

	m, err := bot.Send(msg)
	if err != nil {
		fmt.Println(err)
	}

	typing := true
	go func() {
		for typing {
			chatAction := tgbotapi.NewChatAction(message.Chat.ID, tgbotapi.ChatTyping)
			_, _ = bot.Send(chatAction)
			time.Sleep(time.Second * 4)
		}
	}()

	photo := message.Photo[len(message.Photo)-1] // Get the highest resolution photo
	fileID := photo.FileID

	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		ErrorBot(bot, message.Chat.ID, err)
		typing = false
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", bot.Token, file.FilePath)

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		ErrorBot(bot, message.Chat.ID, err)
		typing = false
		return
	}
	defer resp.Body.Close()

	imgBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		ErrorBot(bot, message.Chat.ID, err)
		typing = false
		return
	}

	cid, _, err := uploader.PostFile(imgBytes, w)
	if err != nil {
		ErrorBot(bot, message.Chat.ID, err)
		typing = false
		return
	}

	// TODO: add back view on chain
	//var URLEncoding = base64.StdEncoding
	//u1, err := url2.Parse(fmt.Sprintf("https://testnet-api.jackalprotocol.com/jackal/canine-chain/storage/files/merkle/%s", URLEncoding.EncodeToString(merkle)))
	//if err != nil {
	//	ErrorBot(bot, message.Chat.ID, err)
	//	typing = false
	//	return
	//}
	u2, err := url2.Parse(fmt.Sprintf("https://gateway.pinata.cloud/ipfs/%s", cid))
	if err != nil {
		ErrorBot(bot, message.Chat.ID, err)
		typing = false
		return
	}
	var inlineKeyboard = tgbotapi.NewInlineKeyboardMarkup(
		//tgbotapi.NewInlineKeyboardRow(
		//	tgbotapi.NewInlineKeyboardButtonURL("View On Chain", u1.String()),
		//),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("View on IPFS", u2.String()),
		),
	)

	del := tgbotapi.NewDeleteMessage(message.Chat.ID, m.MessageID)
	_, err = bot.Send(del)
	if err != nil {
		fmt.Println(err)
	}

	editMsg := tgbotapi.NewMessage(message.Chat.ID, "Your photo was successfully uploaded to Jackal")
	editMsg.ReplyToMessageID = message.MessageID
	editMsg.ReplyMarkup = inlineKeyboard
	_, err = bot.Send(editMsg)
	if err != nil {
		fmt.Println(err)
	}
	typing = false
}

func ErrorBot(bot *tgbotapi.BotAPI, chatId int64, err error) {
	msg := tgbotapi.NewMessage(chatId, fmt.Errorf("jackalbot error | %w", err).Error())
	_, err = bot.Send(msg)
	if err != nil {
		fmt.Println(err)
	}
}

func PollUpdates(bot *tgbotapi.BotAPI, db *badger.DB, updateConfig tgbotapi.UpdateConfig) {
	updates := bot.GetUpdatesChan(updateConfig)
	for u := range updates {
		if u.Message == nil {
			continue
		}

		message := u.Message

		go func() {

			user := message.From

			wallet, _ := LoadWallet(db, user.ID)

			if message.Command() == "wallet" {
				newWallet := wallet == nil
				if wallet == nil {
					w, err := CreateWallet(db, user.ID)
					if err != nil {
						ErrorBot(bot, message.Chat.ID, err)
						return
					}
					wallet = w
				}
				var balance int64
				if !newWallet {
					bank := banktypes.NewQueryClient(wallet.Client.GRPCConn)
					bal, err := bank.Balance(context.Background(), &banktypes.QueryBalanceRequest{
						Address: wallet.AccAddress(),
						Denom:   "ujkl",
					})
					if err != nil {
						ErrorBot(bot, message.Chat.ID, err)
						return
					}
					balance = bal.Balance.Amount.Int64()
				}

				msgText := fmt.Sprintf("Your Jackal wallet address is `%s`\n\nYour balance is *%f* $JKL", wallet.AccAddress(), float64(balance)/float64(1_000_000))
				if newWallet {
					msgText = fmt.Sprintf("Your Jackal wallet address is `%s`. Please send some JKL tokens to this wallet to get started.", wallet.AccAddress())
				}

				msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
				msg.ParseMode = tgbotapi.ModeMarkdown
				msg.ReplyToMessageID = message.MessageID
				_, err := bot.Send(msg)
				if err != nil {
					fmt.Println(err)
				}
				return
			}

			if message.Command() == "balance" {

				var balance int64
				bank := banktypes.NewQueryClient(wallet.Client.GRPCConn)
				bal, err := bank.Balance(context.Background(), &banktypes.QueryBalanceRequest{
					Address: wallet.AccAddress(),
					Denom:   "ujkl",
				})
				if err != nil {
					ErrorBot(bot, message.Chat.ID, err)
					return
				}
				balance = bal.Balance.Amount.Int64()

				msgText := fmt.Sprintf("Your balance is *%f* $JKL", float64(balance)/float64(1_000_000))

				msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
				msg.ParseMode = tgbotapi.ModeMarkdown
				msg.ReplyToMessageID = message.MessageID
				_, err = bot.Send(msg)
				if err != nil {
					fmt.Println(err)
				}
				return
			}

			if message.Command() == "upload" {
				msg := tgbotapi.NewMessage(message.Chat.ID, "Upload an image now...")
				tmpMsg, _ := bot.Send(msg)
				userUploads[user.ID] = tmpMsg.MessageID
			}

			if message.Command() == "start" {
				msg := tgbotapi.NewMessage(message.Chat.ID, "👋🏻 Hi there!\n\nJackal Protocol Bot is your go-to tool for secure file uploads directly through Telegram!\n\n❓ WHAT CAN I DO ❓\n\n- /wallet will create a new wallet for you and can be used to see your address any time!\n- /balance will show you your accounts $JKL balance.\n- /upload will let you upload files to the Jackal Protocol.\n- /withdraw `{address}` will send all the tokens in this wallet to the specified address.")
				msg.ParseMode = tgbotapi.ModeMarkdown
				msg.ReplyToMessageID = message.MessageID

				_, _ = bot.Send(msg)
			}

			if message.Command() == "withdraw" {
				address := message.CommandArguments()
				if len(address) == 0 {
					msg := tgbotapi.NewMessage(message.Chat.ID, "Must provide an address. EX: /withdraw `{jkl1...}`")
					msg.ParseMode = tgbotapi.ModeMarkdown
					_, _ = bot.Send(msg)
					return
				}
				adr, err := sdk.AccAddressFromBech32(address)
				if err != nil {
					msg := tgbotapi.NewMessage(message.Chat.ID, "I can't find that account, you likely typed in the wrong address.")
					_, _ = bot.Send(msg)
					return
				}
				bank := banktypes.NewQueryClient(wallet.Client.GRPCConn)
				bal, err := bank.Balance(context.Background(), &banktypes.QueryBalanceRequest{
					Address: wallet.AccAddress(),
					Denom:   "ujkl",
				})
				if err != nil {
					ErrorBot(bot, message.Chat.ID, err)
					return
				}
				fmt.Println(bal.Balance.Denom)
				fmt.Println(bal.Balance.Amount.Int64())

				myAdr, _ := sdk.AccAddressFromBech32(wallet.AccAddress())
				b := *bal.Balance
				b = b.SubAmount(sdk.NewInt(1_000))
				fmt.Println(b.Amount.Int64())

				m := banktypes.NewMsgSend(myAdr, adr, sdk.NewCoins(b))
				err = m.ValidateBasic()
				if err != nil {
					ErrorBot(bot, message.Chat.ID, err)
					return
				}
				res, err := uploader.PostWithFee(m, wallet)
				if err != nil {
					ErrorBot(bot, message.Chat.ID, err)
					return
				}
				var inlineKeyboard = tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonURL("View on chain", fmt.Sprintf("https://staking-explorer.com/transaction.php?chain=jackal&tx=%s", res.TxHash)),
					),
				)
				fmt.Println(res)
				msg := tgbotapi.NewMessage(message.Chat.ID, "Sent!")
				msg.ParseMode = tgbotapi.ModeMarkdown
				msg.ReplyToMessageID = message.MessageID
				msg.ReplyMarkup = inlineKeyboard
				_, _ = bot.Send(msg)
			}

			if userUploads[user.ID] > 0 {
				if message.Photo != nil {
					if wallet == nil {
						msg := tgbotapi.NewMessage(message.Chat.ID, "You need to create a Jackal wallet before starting, please use `/start`.")
						msg.ReplyToMessageID = message.MessageID
						_, _ = bot.Send(msg)
						return
					}

					bank := banktypes.NewQueryClient(wallet.Client.GRPCConn)
					bal, err := bank.Balance(context.Background(), &banktypes.QueryBalanceRequest{
						Address: wallet.AccAddress(),
						Denom:   "ujkl",
					})
					if err != nil {
						msg := tgbotapi.NewMessage(message.Chat.ID, "I can't find your account, you likely haven't sent any tokens to it yet.")
						_, _ = bot.Send(msg)
					}
					balance := bal.Balance.Amount.Int64()

					size := int64(message.Photo[len(message.Photo)-1].FileSize)
					kbs := size / 1024
					price := ((float64(15*12*100) / 1024.0) / 1024.0) / 1024.0 // price for kb

					totalPrice := price * float64(kbs)
					JKLPrice := totalPrice / 0.24
					if balance < int64(JKLPrice*1_000_000) {
						msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("You don't have enough JKL tokens. You need at least %f JKL", JKLPrice*1.25))
						_, _ = bot.Send(msg)
						return
					}

					HandlePhoto(bot, userUploads[user.ID], wallet, message)
					userUploads[user.ID] = 0
				}
			}

		}()

	}
}

func LoadWallet(db *badger.DB, id int64) (*wallet.Wallet, error) {
	var w *wallet.Wallet
	userIDString := fmt.Sprintf("user:%d", id)
	err := db.View(func(txn *badger.Txn) error {

		seedItem, err := txn.Get([]byte(userIDString))
		if err != nil {
			return err
		}

		err = seedItem.Value(func(val []byte) error {

			wal, err := jWallet.CreateWallet(string(val), "m/44'/118'/0'/0/0", types.ChainConfig{
				Bech32Prefix:  "jkl",
				RPCAddr:       RPC,
				GRPCAddr:      GRPC,
				GasPrice:      "0.02ujkl",
				GasAdjustment: 1.5,
			})
			if err != nil {
				return err
			}

			w = wal

			return nil
		})
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return w, nil
}

func CreateWallet(db *badger.DB, id int64) (*wallet.Wallet, error) {

	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return nil, err
	}

	seed, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, err
	}

	var w *wallet.Wallet
	userIDString := fmt.Sprintf("user:%d", id)
	err = db.Update(func(txn *badger.Txn) error {

		wal, err := jWallet.CreateWallet(seed, "m/44'/118'/0'/0/0", types.ChainConfig{
			Bech32Prefix:  "jkl",
			RPCAddr:       RPC,
			GRPCAddr:      GRPC,
			GasPrice:      "0.02ujkl",
			GasAdjustment: 1.5,
		})
		if err != nil {
			return err
		}
		w = wal

		err = txn.Set([]byte(userIDString), []byte(seed))
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return w, nil
}

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	db, err := badger.Open(badger.DefaultOptions("badger.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	bot, err := tgbotapi.NewBotAPI(os.Getenv("TG_KEY"))
	if err != nil {
		panic(err)
	}

	//bot.Debug = true

	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 30

	//q := uploader.NewQueue(w)
	//q.Listen()

	PollUpdates(bot, db, updateConfig)
}
