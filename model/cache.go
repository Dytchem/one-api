package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/random"
)

// dyt-108: 令牌不存在（负缓存命中时返回的稳定错误值）。
// 调用方一律按"令牌无效"处理，与 gorm.ErrRecordNotFound 语义等价。
var ErrTokenNotExist = errors.New("token not exist")

var (
	TokenCacheSeconds         = config.SyncFrequency
	UserId2GroupCacheSeconds  = config.SyncFrequency
	UserId2QuotaCacheSeconds  = config.SyncFrequency
	UserId2StatusCacheSeconds = config.SyncFrequency
	GroupModelsCacheSeconds   = config.SyncFrequency
)

// ---- dyt-100: 进程内 TTL 缓存（Redis 未启用时生效，语义与 Redis 路径一致：SyncFrequency 窗口）----

type memCacheItem[T any] struct {
	value    T
	expireAt time.Time
}

type memCache[T any] struct {
	mu    sync.Mutex
	items map[string]memCacheItem[T]
	ttl   time.Duration
}

func newMemCache[T any](ttl time.Duration) *memCache[T] {
	return &memCache[T]{items: make(map[string]memCacheItem[T]), ttl: ttl}
}

func (c *memCache[T]) Get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok || time.Now().After(it.expireAt) {
		if ok {
			delete(c.items, key)
		}
		var zero T
		return zero, false
	}
	return it.value, true
}

func (c *memCache[T]) Set(key string, v T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) > 100000 { // 防膨胀：超过阈值顺带清理过期项
		now := time.Now()
		for k, it := range c.items {
			if now.After(it.expireAt) {
				delete(c.items, k)
			}
		}
	}
	c.items[key] = memCacheItem[T]{value: v, expireAt: time.Now().Add(c.ttl)}
}

func (c *memCache[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Clear 清空全部缓存项（用于"整表变了"这类无法逐 key 失效的场景）
func (c *memCache[T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]memCacheItem[T])
}

var (
	tokenMemCache         = newMemCache[*Token](time.Duration(TokenCacheSeconds) * time.Second)
	userGroupMemCache     = newMemCache[string](time.Duration(UserId2GroupCacheSeconds) * time.Second)
	userEnabledMemCache   = newMemCache[bool](time.Duration(UserId2StatusCacheSeconds) * time.Second)
	channelStatusMemCache = newMemCache[int](time.Duration(config.SyncFrequency) * time.Second)
	// dyt-122: ability 查询结果缓存（key = group|model）。
	// Distribute() 的「未固定渠道」分支每个中继请求都要跑一次
	// GetTopSatisfiedAbilities（带 MAX(priority) 子查询的 SELECT）——
	// fork 用健康感知选路取代了上游带缓存的 CacheGetRandomSatisfiedChannel，
	// 这条查询于是落到了热路径上。渠道/ability 写路径经 InitChannelCache
	// 主动失效（见 ClearAbilityCache），另有 10s TTL 兜底：即便某条写路径
	// 漏了失效，陈旧窗口也只有 10s。
	abilityMemCache = newMemCache[[]Ability](10 * time.Second)
	// dyt-108: 无效 token key 的负缓存。
	// 原实现：CacheGetTokenByKey 查库失败直接 return err，**不缓存失败结果**，
	// 于是任何随机 `sk-` 键的请求都会打到数据库（找不到也要查一次库）。
	// 公网可无上限铸造随机键 ⇒ 典型 DB 放大/DoS 面
	// （v106 已加 /v1 限流兜底，本条是让缓存真正生效的正解）。
	// TTL 取较短值：既要挡住洪峰，又要在令牌新建/变更后尽快可见
	// （相关写路径会主动 Delete，见 DeleteTokenMemCache）。
	invalidTokenMemCache = newMemCache[bool](30 * time.Second)
)

// DeleteUserMemCache: 用户变更（更新/封禁/删除/额度/角色）后调用，失效进程内缓存
func DeleteUserMemCache(id int) {
	key := strconv.Itoa(id)
	userGroupMemCache.Delete(key)
	userEnabledMemCache.Delete(key)
}

// DeleteTokenMemCache: 令牌变更（增删改/状态）后调用，按 key 失效
func DeleteTokenMemCache(key string) {
	tokenMemCache.Delete(key)
	// dyt-108: 连带清掉负缓存。否则"先被随机键撞出负缓存、之后才新建同名令牌"
	// 的场景会在 TTL 内一直报令牌无效（改用已有 key 重建令牌时就会踩到）。
	invalidTokenMemCache.Delete(key)
}

// DeleteChannelMemCache: 渠道变更后调用（状态/权重/配置），供健康路由缓存使用
func DeleteChannelMemCache(id int) {
	channelStatusMemCache.Delete(strconv.Itoa(id))
}

func CacheGetTokenByKey(key string) (*Token, error) {
	keyCol := "`key`"
	if common.UsingPostgreSQL {
		keyCol = `"key"`
	}
	var token Token
	if !common.RedisEnabled {
		// dyt-100: 进程内 TTL 缓存（600s 窗口，与 Redis 路径一致；token 变更处主动失效）
		if v, ok := tokenMemCache.Get(key); ok {
			return v, nil
		}
		// dyt-108: 命中负缓存直接返回，避免无效键每次都查库（随机 sk- 洪峰放大 DB）
		if _, ok := invalidTokenMemCache.Get(key); ok {
			return nil, ErrTokenNotExist
		}
		err := DB.Where(keyCol+" = ?", key).First(&token).Error
		if err != nil {
			// dyt-108: 仅对"确实不存在"做负缓存。其它错误（连接失败/超时等）不缓存，
			// 否则数据库抖动会把短暂的失败固化成 30s 的假"令牌无效"。
			if errors.Is(err, gorm.ErrRecordNotFound) {
				invalidTokenMemCache.Set(key, true)
			}
			return nil, err
		}
		// 深拷贝：Models/Subnet 是指针字段，防止调用方修改污染缓存
		copy := token
		if token.Models != nil {
			m := *token.Models
			copy.Models = &m
		}
		if token.Subnet != nil {
			s := *token.Subnet
			copy.Subnet = &s
		}
		tokenMemCache.Set(key, &copy)
		return &copy, nil
	}
	tokenObjectString, err := common.RedisGet(fmt.Sprintf("token:%s", key))
	if err != nil {
		err := DB.Where(keyCol+" = ?", key).First(&token).Error
		if err != nil {
			return nil, err
		}
		jsonBytes, err := json.Marshal(token)
		if err != nil {
			return nil, err
		}
		err = common.RedisSet(fmt.Sprintf("token:%s", key), string(jsonBytes), time.Duration(TokenCacheSeconds)*time.Second)
		if err != nil {
			logger.SysError("Redis set token error: " + err.Error())
		}
		return &token, nil
	}
	err = json.Unmarshal([]byte(tokenObjectString), &token)
	return &token, err
}

func CacheGetUserGroup(id int) (group string, err error) {
	if !common.RedisEnabled {
		// dyt-100: 进程内 TTL 缓存
		if v, ok := userGroupMemCache.Get(strconv.Itoa(id)); ok {
			return v, nil
		}
		group, err = GetUserGroup(id)
		if err == nil {
			userGroupMemCache.Set(strconv.Itoa(id), group)
		}
		return group, err
	}
	group, err = common.RedisGet(fmt.Sprintf("user_group:%d", id))
	if err != nil {
		group, err = GetUserGroup(id)
		if err != nil {
			return "", err
		}
		err = common.RedisSet(fmt.Sprintf("user_group:%d", id), group, time.Duration(UserId2GroupCacheSeconds)*time.Second)
		if err != nil {
			logger.SysError("Redis set user group error: " + err.Error())
		}
	}
	return group, err
}

func fetchAndUpdateUserQuota(ctx context.Context, id int) (quota int64, err error) {
	quota, err = GetUserQuota(id)
	if err != nil {
		return 0, err
	}
	err = common.RedisSet(fmt.Sprintf("user_quota:%d", id), fmt.Sprintf("%d", quota), time.Duration(UserId2QuotaCacheSeconds)*time.Second)
	if err != nil {
		logger.Error(ctx, "Redis set user quota error: "+err.Error())
	}
	return
}

func CacheGetUserQuota(ctx context.Context, id int) (quota int64, err error) {
	if !common.RedisEnabled {
		return GetUserQuota(id)
	}
	quotaString, err := common.RedisGet(fmt.Sprintf("user_quota:%d", id))
	if err != nil {
		return fetchAndUpdateUserQuota(ctx, id)
	}
	quota, err = strconv.ParseInt(quotaString, 10, 64)
	if err != nil {
		return 0, nil
	}
	if quota <= config.PreConsumedQuota { // when user's quota is less than pre-consumed quota, we need to fetch from db
		logger.Infof(ctx, "user %d's cached quota is too low: %d, refreshing from db", quota, id)
		return fetchAndUpdateUserQuota(ctx, id)
	}
	return quota, nil
}

func CacheUpdateUserQuota(ctx context.Context, id int) error {
	if !common.RedisEnabled {
		return nil
	}
	quota, err := CacheGetUserQuota(ctx, id)
	if err != nil {
		return err
	}
	err = common.RedisSet(fmt.Sprintf("user_quota:%d", id), fmt.Sprintf("%d", quota), time.Duration(UserId2QuotaCacheSeconds)*time.Second)
	return err
}

func CacheDecreaseUserQuota(id int, quota int64) error {
	if !common.RedisEnabled {
		return nil
	}
	err := common.RedisDecrease(fmt.Sprintf("user_quota:%d", id), int64(quota))
	return err
}

func CacheIsUserEnabled(userId int) (bool, error) {
	// dyt-96: Redis 未启用时每次回查数据库，无窗口。
	// 启用时缓存窗口 = SyncFrequency（默认 600s）：本服务内封禁/启用会同步清缓存，
	// 直接改库/多实例场景下禁用最长延迟一个窗口
	if !common.RedisEnabled {
		// dyt-100: 进程内 TTL 缓存（用户封禁/启用/删除路径主动失效）
		if v, ok := userEnabledMemCache.Get(strconv.Itoa(userId)); ok {
			return v, nil
		}
		userEnabled, err := IsUserEnabled(userId)
		if err == nil {
			userEnabledMemCache.Set(strconv.Itoa(userId), userEnabled)
		}
		return userEnabled, err
	}
	enabled, err := common.RedisGet(fmt.Sprintf("user_enabled:%d", userId))
	if err == nil {
		return enabled == "1", nil
	}

	userEnabled, err := IsUserEnabled(userId)
	if err != nil {
		return false, err
	}
	enabled = "0"
	if userEnabled {
		enabled = "1"
	}
	err = common.RedisSet(fmt.Sprintf("user_enabled:%d", userId), enabled, time.Duration(UserId2StatusCacheSeconds)*time.Second)
	if err != nil {
		logger.SysError("Redis set user enabled error: " + err.Error())
	}
	return userEnabled, err
}

func CacheGetGroupModels(ctx context.Context, group string) ([]string, error) {
	if !common.RedisEnabled {
		return GetGroupModels(ctx, group)
	}
	modelsStr, err := common.RedisGet(fmt.Sprintf("group_models:%s", group))
	if err == nil {
		return strings.Split(modelsStr, ","), nil
	}
	models, err := GetGroupModels(ctx, group)
	if err != nil {
		return nil, err
	}
	err = common.RedisSet(fmt.Sprintf("group_models:%s", group), strings.Join(models, ","), time.Duration(GroupModelsCacheSeconds)*time.Second)
	if err != nil {
		logger.SysError("Redis set group models error: " + err.Error())
	}
	return models, nil
}

var group2model2channels map[string]map[string][]*Channel
var channelId2channel map[int]*Channel // dyt-100: 内存缓存启用时的渠道快照（含 key），按 id 索引
var channelSyncLock sync.RWMutex

// CacheGetChannelById: 内存缓存命中时直接返回快照（免查库），未开启/未命中回退 DB。
// 语义与 CacheGetRandomSatisfiedChannel 一致（最多 SyncFrequency 内一致）
func CacheGetChannelById(id int, selectAll bool) (*Channel, error) {
	if config.MemoryCacheEnabled {
		channelSyncLock.RLock()
		ch, ok := channelId2channel[id]
		channelSyncLock.RUnlock()
		if ok && ch != nil {
			return ch, nil
		}
	}
	return GetChannelById(id, selectAll)
}

// ClearAbilityCache: 渠道/ability 变更后失效 ability 查询缓存
func ClearAbilityCache() {
	abilityMemCache.Clear()
}

// CacheGetChannelsByIds: 批量取渠道（dyt-122）。
// distributor 的健康选路一次最多要 3 个渠道，原实现逐个 CacheGetChannelById；
// 在 MEMORY_CACHE_ENABLED 未开启（本部署即如此）时那就是 3 条 SELECT，
// 现在最多 1 条 IN 查询（开启内存缓存时则直接命中快照）。
func CacheGetChannelsByIds(ids []int) (map[int]*Channel, error) {
	res := make(map[int]*Channel, len(ids))
	if len(ids) == 0 {
		return res, nil
	}
	var missing []int
	if config.MemoryCacheEnabled {
		channelSyncLock.RLock()
		for _, id := range ids {
			if ch, ok := channelId2channel[id]; ok && ch != nil {
				res[id] = ch
			} else {
				missing = append(missing, id)
			}
		}
		channelSyncLock.RUnlock()
	} else {
		missing = ids
	}
	if len(missing) == 0 {
		return res, nil
	}
	var channels []*Channel
	if err := DB.Where("id IN ?", missing).Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, ch := range channels {
		res[ch.Id] = ch
	}
	return res, nil
}

func InitChannelCache() {
	newChannelId2channel := make(map[int]*Channel)
	var channels []*Channel
	DB.Where("status = ?", ChannelStatusEnabled).Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
	}
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]*Channel)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]*Channel)
	}
	for _, channel := range channels {
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]*Channel, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return channels[i].GetPriority() > channels[j].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	channelId2channel = newChannelId2channel // dyt-100: 渠道快照一并发布
	channelSyncLock.Unlock()
	ClearAbilityCache() // dyt-122: 渠道快照变了，ability 选路结果一并失效
	logger.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		logger.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

func CacheGetRandomSatisfiedChannel(group string, model string, ignoreFirstPriority bool) (*Channel, error) {
	if !config.MemoryCacheEnabled {
		return GetRandomSatisfiedChannel(group, model, ignoreFirstPriority)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	channels := group2model2channels[group][model]
	if len(channels) == 0 {
		return nil, errors.New("channel not found")
	}
	endIdx := len(channels)
	// choose by priority
	firstChannel := channels[0]
	if firstChannel.GetPriority() > 0 {
		for i := range channels {
			if channels[i].GetPriority() != firstChannel.GetPriority() {
				endIdx = i
				break
			}
		}
	}
	idx := random.RandRange(0, endIdx)
	if ignoreFirstPriority {
		if endIdx < len(channels) { // which means there are more than one priority
			idx = random.RandRange(endIdx, len(channels))
		}
	}
	return channels[idx], nil
}
