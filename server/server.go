package server

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/hightrex/crypto-exchange/orderbook"
	"github.com/labstack/echo/v4"
)

const (
	MarketOrder        OrderType = "MARKET"
	LimitOrder         OrderType = "LIMIT"
	exchangePrivateKey           = "36ef7fb54e4b15fc3b4bd4ad3a580b83fe2d1b7b7abd82914b9a9b1403cf6bd6"
	MarketETH          Market    = "ETH"
)

type (
	OrderType string
	Market    string

	PlaceOrderRequest struct {
		UserID int64
		Type   OrderType //Limit or market
		Bid    bool
		Size   float64
		Price  float64
		Market Market
	}
	Order struct {
		UserID    int64
		ID        int64
		Price     float64
		Size      float64
		Bid       bool
		Timestamp int64
	}
	OrderbookData struct {
		TotalBidVolume float64
		TotalAskVolume float64
		Asks           []*Order
		Bids           []*Order
	}
	MatchedOrder struct {
		UserID int64
		Price  float64
		Size   float64
		ID     int64
	}

	APIError struct {
		Error string
	}
)

func StartServer() {

	e := echo.New()

	e.HTTPErrorHandler = httpErrorHandler

	client, err := ethclient.Dial("http://localhost:7545")
	if err != nil {
		log.Fatal(err)
	}

	ex, err := NewExchange(exchangePrivateKey, client)
	if err != nil {
		log.Fatal(err)
	}

	pkStr1 := "57ab556e51237665cbbe0d7ac06adbc058ab141cde15e915e200449d9fc57170"
	user1 := NewUser(pkStr1, 1)
	ex.Users[user1.ID] = user1
	sellerAddressSrt := "0xec7973bC53d1504Bd14205012EE67602D2022C49"
	sellerBalance, err1 := client.BalanceAt(context.Background(), common.HexToAddress(sellerAddressSrt), nil)
	if err1 != nil {
		log.Fatal(err1)
	}
	fmt.Print("seller balance : ")
	fmt.Println(sellerBalance)

	pkStr2 := "43c7c471cb85bb66b8ecd99aa840128fb6b85a940380d48ddfd764281ce3a317"
	user2 := NewUser(pkStr2, 2)
	ex.Users[user2.ID] = user2
	buyerAddressSrt := "0x847d38cf14ef77e0A31E4773b03Bf3a78d18e74F"
	buyerBalance, err2 := client.BalanceAt(context.Background(), common.HexToAddress(buyerAddressSrt), nil)
	if err2 != nil {
		log.Fatal(err2)
	}
	fmt.Print("buyer balance : ")
	fmt.Println(buyerBalance)

	john3PK := "7fbc4773bb234745d8e3aaab6e01d3044b1248a71208941988c771eebb9fed91"
	john := NewUser(john3PK, 3)
	ex.Users[john.ID] = john
	johnAddressStr := "0x2f70A4A2F75BaFF71d1563273Fa9630250a9dB55"
	johnBalance, err3 := client.BalanceAt(context.Background(), common.HexToAddress(johnAddressStr), nil)
	if err3 != nil {
		log.Fatal(err3)
	}
	fmt.Print("john balance : ")
	fmt.Println(johnBalance)

	e.POST("/order", ex.handlePlaceOrder)

	e.GET("/trades/:market", ex.handleGetTrades)
	e.GET("/order/:userID", ex.handleGetOrders)
	e.GET("/book/:market", ex.handleGetBook)
	e.GET("/book/:market/bid", ex.handleGetBestBid)
	e.GET("/book/:market/ask", ex.handleGetBestAsk)

	e.DELETE("/order/:id", ex.cancelOrder)
	e.Start(":3000")

}

type User struct {
	ID         int64
	PrivateKey *ecdsa.PrivateKey
}

func NewUser(privKey string, id int64) *User {
	pk, err := crypto.HexToECDSA(privKey)
	if err != nil {
		panic(err)
	}
	return &User{
		ID:         id,
		PrivateKey: pk,
	}
}

func httpErrorHandler(err error, c echo.Context) {
	fmt.Println(err)
}

type (
	Exchange struct {
		Client *ethclient.Client
		mu     sync.RWMutex
		Users  map[int64]*User
		// Orders maps a user to his orders
		Orders     map[int64][]*orderbook.Order
		PrivateKey *ecdsa.PrivateKey
		orderbooks map[Market]*orderbook.Orderbook
	}
)

func NewExchange(privateKey string, client *ethclient.Client) (*Exchange, error) {
	orderbooks := make(map[Market]*orderbook.Orderbook)
	orderbooks[MarketETH] = orderbook.NewOrderbook()

	pk, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return nil, err
	}

	return &Exchange{
		Client: client,
		Users:  make(map[int64]*User),
		Orders: make(map[int64][]*orderbook.Order),
		//orders:     make(map[int64]int64),
		PrivateKey: pk,
		orderbooks: orderbooks,
	}, nil
}

type GetOrdersResponse struct {
	Asks []Order
	Bids []Order
}

func (ex *Exchange) handleGetTrades(c echo.Context) error {
	market := Market(c.Param("market"))

	ob, ok := ex.orderbooks[market]
	if !ok {
		return c.JSON(http.StatusBadRequest, APIError{Error: "Orderbook not found"})

	}
	return c.JSON(http.StatusOK, ob.Trades)
}

func (ex *Exchange) handleGetOrders(c echo.Context) error {
	userIDStr := c.Param("userID")
	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		return err
	}

	ex.mu.RLock()
	orderbookOrders := ex.Orders[int64(userID)]
	orderResp := &GetOrdersResponse{
		Asks: []Order{},
		Bids: []Order{},
	}

	for i := 0; i < len(orderbookOrders); i++ {
		// it could be that the order is getting filled even though its included in this response
		// We must double check if the limitss is not nil
		if orderbookOrders[i].Limit == nil {
			continue
		}

		order := Order{
			ID:        orderbookOrders[i].ID,
			UserID:    orderbookOrders[i].UserID,
			Price:     orderbookOrders[i].Limit.Price,
			Size:      orderbookOrders[i].Size,
			Timestamp: orderbookOrders[i].Timestamp,
			Bid:       orderbookOrders[i].Bid,
		}

		if order.Bid {
			orderResp.Bids = append(orderResp.Bids, order)
		} else {
			orderResp.Asks = append(orderResp.Asks, order)
		}
	}
	ex.mu.RUnlock()

	return c.JSON(http.StatusOK, orderResp)

}

func (ex *Exchange) handleGetBook(c echo.Context) error {
	market := Market(c.Param("market"))
	ob, ok := ex.orderbooks[market]
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]any{"msg": "market not found"})

	}

	orderbookData := OrderbookData{
		TotalBidVolume: ob.BidTotalVolume(),
		TotalAskVolume: ob.AskTotalVolume(),
		Asks:           []*Order{},
		Bids:           []*Order{},
	}
	for _, limit := range ob.Asks() {
		for _, order := range limit.Orders {
			o := Order{
				UserID:    order.UserID,
				ID:        order.ID,
				Price:     limit.Price,
				Size:      order.Size,
				Bid:       order.Bid,
				Timestamp: order.Timestamp,
			}
			orderbookData.Asks = append(orderbookData.Asks, &o)

		}

	}
	for _, limit := range ob.Bids() {
		for _, order := range limit.Orders {
			o := Order{
				UserID:    order.UserID,
				ID:        order.ID,
				Price:     limit.Price,
				Size:      order.Size,
				Bid:       order.Bid,
				Timestamp: order.Timestamp,
			}
			orderbookData.Bids = append(orderbookData.Bids, &o)

		}

	}

	return c.JSON(http.StatusOK, orderbookData)

}

type PriceResponse struct {
	Price float64
}

func (ex *Exchange) handleGetBestBid(c echo.Context) error {

	market := Market(c.Param("market"))
	ob := ex.orderbooks[market]

	if len(ob.Bids()) == 0 {
		return fmt.Errorf("the bids are empty")
	}
	bestBidPrice := ob.Bids()[0].Price
	pr := PriceResponse{
		Price: bestBidPrice,
	}

	return c.JSON(http.StatusOK, pr)
}

func (ex *Exchange) handleGetBestAsk(c echo.Context) error {

	market := Market(c.Param("market"))
	ob := ex.orderbooks[market]

	if len(ob.Asks()) == 0 {
		return fmt.Errorf("the asks are empty")
	}
	bestAskPrice := ob.Asks()[0].Price
	pr := PriceResponse{
		Price: bestAskPrice,
	}

	return c.JSON(http.StatusOK, pr)
}

func (ex *Exchange) cancelOrder(c echo.Context) error {
	idStr := c.Param("id")
	id, _ := strconv.Atoi(idStr)

	ob := ex.orderbooks[MarketETH]
	order := ob.Orders[int64(id)]
	ob.CancelOrder(order)

	//log.Println("order canceled id => ", id)

	return c.JSON(200, map[string]any{"msg": "order deleted"})
}

func (ex *Exchange) handlePlaceMarketOrder(market Market, order *orderbook.Order) ([]orderbook.Match, []*MatchedOrder) {
	ob := ex.orderbooks[market]
	matches := ob.PlaceMarketOrder(order)
	matchedOrders := make([]*MatchedOrder, len(matches))

	isBid := false
	if order.Bid {
		isBid = true
	}
	totalSizeFilled := 0.0
	sumPrice := 0.0
	for i := 0; i < len(matchedOrders); i++ {
		id := matches[i].Bid.ID
		limitUserID := matches[i].Bid.UserID
		if isBid {
			limitUserID = matches[i].Ask.UserID
			id = matches[i].Ask.ID
		}
		matchedOrders[i] = &MatchedOrder{
			UserID: limitUserID,
			ID:     id,
			Size:   matches[i].SizeFilled,
			Price:  matches[i].Price,
		}

		totalSizeFilled += matches[i].SizeFilled
		sumPrice += matches[i].Price
	}

	avgPrice := sumPrice / float64(len(matches))

	log.Printf("filled market order => %d | size : [%.2f] | avgPrice : [%.2f]", order.ID, totalSizeFilled, avgPrice)

	newOrderMap := make(map[int64][]*orderbook.Order)

	ex.mu.Lock()
	for userID, orderbookOrders := range ex.Orders {
		for i := 0; i < len(orderbookOrders); i++ {
			//if the order is not filled we place it in the map copy
			// this means that the size of the order = 0
			if !orderbookOrders[i].Isfilled() {
				newOrderMap[userID] = append(newOrderMap[userID], orderbookOrders[i])
			}
		}
	}

	ex.Orders = newOrderMap
	ex.mu.Unlock()

	return matches, matchedOrders
}

func (ex *Exchange) handlePlaceLimitOrder(market Market, price float64, order *orderbook.Order) error {
	ob := ex.orderbooks[market]
	ob.PlaceLimitOrder(price, order)

	//keep track of the user orders
	ex.mu.Lock()
	ex.Orders[order.UserID] = append(ex.Orders[order.UserID], order)
	ex.mu.Unlock()
	//log.Printf("new LIMIT order => type: [%t] | price [%.2f] | size [%.2f]", order.Bid, order.Limit.Price, order.Size)

	return nil
}

type PlaceOrderResponse struct {
	OrderID int64
}

func (ex *Exchange) handlePlaceOrder(c echo.Context) error {

	var placeOrderData PlaceOrderRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&placeOrderData); err != nil {
		return err
	}

	market := Market(placeOrderData.Market)
	order := orderbook.NewOrder(placeOrderData.Bid, placeOrderData.Size, placeOrderData.UserID)

	// Limit orders
	if placeOrderData.Type == LimitOrder {
		if err := ex.handlePlaceLimitOrder(market, placeOrderData.Price, order); err != nil {
			return err
		}

	}

	// Market orders
	if placeOrderData.Type == MarketOrder {
		matches, _ := ex.handlePlaceMarketOrder(market, order)
		if err := ex.handleMatches(matches); err != nil {
			return err
		}

	}

	resp := &PlaceOrderResponse{
		OrderID: order.ID,
	}
	return c.JSON(200, resp)
}
func (ex *Exchange) handleMatches(matches []orderbook.Match) error {

	for _, match := range matches {

		fromUser, ok := ex.Users[match.Ask.UserID]
		if !ok {
			return fmt.Errorf("user not found : %d", match.Ask.UserID)
		}

		toUser, ok := ex.Users[match.Bid.UserID]
		if !ok {
			return fmt.Errorf("user not found : %d", match.Bid.UserID)
		}

		toAddress := crypto.PubkeyToAddress(toUser.PrivateKey.PublicKey)

		/* this is only used for the fees
		exchangePubKey := ex.PrivateKey.Public()
		publicKeyECDSA, ok := exchangePubKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
		}
		toAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
		*/

		amount := big.NewInt(int64(match.SizeFilled))

		transferETH(ex.Client, fromUser.PrivateKey, toAddress, amount)
	}

	return nil
}

func transferETH(client *ethclient.Client, fromPrivKey *ecdsa.PrivateKey, to common.Address, amount *big.Int) error {
	ctx := context.Background()
	publicKey := fromPrivKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
	}

	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	nonce, err := client.PendingNonceAt(ctx, fromAddress)
	if err != nil {
		return err
	}
	gasLimit := uint64(21000)
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		log.Fatal(err)
	}
	tx := types.NewTransaction(nonce, to, amount, gasLimit, gasPrice, nil)

	chainID := big.NewInt(1337)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), fromPrivKey)
	if err != nil {
		return err
	}
	return client.SendTransaction(ctx, signedTx)

}
