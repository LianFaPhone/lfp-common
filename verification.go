package common

import (
	. "BastionPay/bas-base/log/zap"
	"BastionPay/bas-tools/sdk.notify.mail"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pborman/uuid"
	"go.uber.org/zap"
	//"github.com/garyburd/redigo/redis"
	"BastionPay/marketing-fission-api/db"
	pkgredis "github.com/go-redis/redis"
	"time"
)

const (
	VerificationPrefix = "verification_"

	// 十分钟
	VerificationCacheTime = 600
	// 最大重试次数
	VerificationRetryNumber = 10

	VerificationTypeEmail   = "email"
	VerificationTypeSms     = "sms"
	VerificationTypeCaptcha = "captcha"
	VerificationTypeGa      = "ga"
)

type Verification struct {
	Id          string      `json:"-"`
	Operating   string      `json:"operating"`
	Type        string      `json:"type"`
	UserID      uint        `json:"user_id"`
	Status      bool        `json:"status"`
	RetryNumber int         `json:"-"`
	RetryCount  int         `json:"retry_count"`
	Value       string      `json:"value"`
	Recipient   string      `json:"recipient"`
	redis       *db.DbRedis `json:"-"`
	record      bool        `json:"-"`
}

func NewVerification(redis *db.DbRedis, operating string, t string) *Verification {
	return &Verification{
		redis:       redis,
		Operating:   operating,
		Type:        t,
		RetryNumber: VerificationRetryNumber,
		record:      true,
	}
}

func (v *Verification) Generate() string {
	v.Id = uuid.NewRandom().String()
	v.Status = false
	return v.Id
}

func (v *Verification) GenerateEmail(userId uint, recipient string, tpl, lang string) (string, error) {
	var err error
	if len(recipient) == 0 || len(tpl) == 0 || len(lang) == 0 {
		return "", errors.New(fmt.Sprintf("nil in one of recipient[%s]tpl[%s]lang[%s]", recipient, tpl, lang))
	}

	v.UserID = userId
	v.Id = uuid.NewRandom().String()
	v.Status = false
	v.Recipient = recipient
	v.Value = RandomDigit(6)

	err = sdk_notify_mail.GNotifySdk.SendMailByGroupName(tpl, lang, []string{recipient}, map[string]interface{}{"key1": v.Value})
	//title, body, err := ParseHtmlTemplate(tpl, &struct {
	//	Value string
	//}{v.Value})
	//if err != nil {
	//	return "", err
	//}
	//
	//err = NewSes(ac).Send(&SesData{
	//	Sender:    sender,
	//	Recipient: recipient,
	//	Subject:   title,
	//	Body:      body,
	//	CharSet:   "UTF-8",
	//})

	if err != nil {
		return "", err
	}

	if err = v.save(); err != nil {
		return "", err
	}

	return v.Id, nil
}

func (v *Verification) GenerateSms(userId uint, recipient string, tpl, lang string) (string, error) {
	var err error

	if len(recipient) == 0 || len(tpl) == 0 || len(lang) == 0 {
		return "", errors.New(fmt.Sprintf("nil in one of recipient[%s]tpl[%s]lang[%s]", recipient, tpl, lang))
	}

	v.UserID = userId
	v.Id = uuid.NewRandom().String()
	v.Status = false
	v.Recipient = recipient
	v.Value = RandomDigit(6)

	err = sdk_notify_mail.GNotifySdk.SendSmsByGroupName(tpl, lang, []string{recipient}, map[string]interface{}{"key1": v.Value})

	//body, err := ParseTextTemplate(tpl, &struct {
	//	Value string
	//}{v.Value})
	//if err != nil {
	//	return "", err
	//}
	//
	//err = NewSns(ac).Send(&SnsData{
	//	Recipient: recipient,
	//	Body:      body,
	//})
	if err != nil {
		return "", err
	}

	if err = v.save(); err != nil {
		return "", err
	}

	return v.Id, nil
}

func (v *Verification) GenerateGA(userId uint, secret string) (string, error) {
	var err error

	v.UserID = userId
	v.Id = uuid.NewRandom().String()
	v.Status = false
	v.Value = secret

	if err = v.save(); err != nil {
		return "", err
	}

	return v.Id, nil
}

func (v *Verification) GenerateCaptcha(userId uint) (string, string, error) {
	var err error

	// 验证码不绑定用户, 默认填0
	v.UserID = userId
	v.Id = uuid.NewRandom().String()
	v.Status = false
	cap := NewCaptcha(v.Id, CaptchaTypeDigit).Generate()
	v.Value = cap.Value

	if err = v.save(); err != nil {
		return "", "", err
	}

	return cap.Id, cap.Captcha, nil
}

//检查状态
func (v *Verification) Check(id string, userId uint, recipient string) (bool, error) {
	var (
		err error
		bol bool
	)

	v.Id = id

	if err = v.read(); err != nil {
		return false, err
	}

	if userId == 0 {
		// 未登录判断接收者 验证状态 验证次数
		if recipient == v.Recipient && v.Status == true && v.RetryCount < v.RetryNumber {
			bol = true
		} else {
			bol = false
		}
	} else {
		// 登录判断uid 验证状态 验证次数
		if userId == v.UserID && v.Status == true && v.RetryCount < v.RetryNumber {
			bol = true
		} else {
			bol = false
		}
	}
	ZapLog().With(zap.String("id", id), zap.Uint("userid", userId), zap.String("recipient", recipient), zap.Any("Verification", v)).Info("check")
	//	glog.Info("id: ", id, "userid: ", userId, "recipient: ", recipient)
	//	glog.Infof("%+v", v)

	return bol, nil
}

// 验证
func (v *Verification) Verify(id string, userId uint, value, recipient string) (bool, error) {
	var (
		b   bool
		err error
	)

	b = false
	v.Id = id

	if err = v.read(); err != nil {
		return false, err
	}
	if !v.record {
		return false, nil
	}

	if (v.Type != VerificationTypeGa) && (recipient != v.Recipient) {
		return false, errors.New("recipient not same")
	}

	// 判断 用户，操作状态， 重试次数
	if userId == v.UserID && v.Status == false && v.RetryCount < v.RetryNumber {
		// 判断 value
		if v.Type == VerificationTypeGa {
			bol, err := NewGA().Verify(v.Value, value)
			if err != nil {
				return false, err
			}

			b = bol
		} else {
			b = v.Value == value
		}
	}

	ZapLog().With(zap.String("id", id), zap.Uint("userid", userId), zap.String("value", value), zap.String("recipient", recipient), zap.Any("Verification", v)).Info("Verify")
	//	glog.Info("id:", id, "userid:", userId, "value", value)
	//	glog.Infof("%+v", v)

	if b {
		v.Status = true
	} else {
		v.RetryCount++
	}

	if err = v.save(); err != nil {
		return false, err
	}

	return v.Status, nil
}

func (v *Verification) save() error {
	var (
		err    error
		expire time.Duration
	)

	// 如果读取时读不到，那么就不做记录
	if !v.record {
		return nil
	}

	b, err := json.Marshal(v)
	if err != nil {
		ZapLog().With(zap.Error(err)).Error("Marshal err")
		return err
	}

	ttl, err := v.redis.GetConn().TTL(VerificationPrefix + v.Id).Result()
	if err != nil {
		ZapLog().With(zap.Error(err)).Error("redis TTL")
		//		glog.Error(err.Error())
		return err
	}
	//if ttl == nil {
	//	return nil
	//}

	if ttl > 0 {
		expire = ttl
	} else {
		expire = time.Duration(time.Second * VerificationCacheTime)
	}
	ZapLog().Info("expire ", zap.Any("expire", expire), zap.Any("ttl", ttl), zap.String("key", VerificationPrefix+v.Id))
	//_, err = v.redis.Do("SET", VerificationPrefix+v.Id, string(b), "EX", expire)
	_, err = v.redis.GetConn().Set(VerificationPrefix+v.Id, (b), expire).Result()
	if err != nil {
		ZapLog().With(zap.Error(err)).Error("redis Set ex")
		return err
	}

	return nil
}

func (v *Verification) read() error {
	var err error
	result, err := v.redis.GetConn().Get(VerificationPrefix + v.Id).Bytes()
	if err == pkgredis.Nil {
		v.record = false
		return nil
	}
	if err != nil {
		ZapLog().With(zap.Error(err)).Error("redis GET")
		//		glog.Error(err.Error())
		v.record = false
		return err
	}
	if result == nil {
		v.record = false
		return nil
	}

	err = json.Unmarshal(result, &v)
	if err != nil {
		ZapLog().With(zap.Error(err)).Error("Unmarshal err")
		//		glog.Error(err.Error())
		return err
	}

	return nil
}
