package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"DistributedDetectionNode/log"
	"DistributedDetectionNode/types"

	"github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var MDB *mongoDB = nil

type mongoDB struct {
	Mongo                 *mongo.Client
	machineConnCollection *mongo.Collection
	machineInfoCollection *mongo.Collection
	machineTMCollection   *mongo.Collection
}

func InitMongo(ctx context.Context, uri, db string, eas int64) error {
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	opts := options.Client().ApplyURI(uri).SetServerAPIOptions(serverAPI)
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		log.Log.Fatalf("Connect mongodb failed: %v", err)
		return err
	}
	if err = client.Ping(ctx, nil); err != nil {
		log.Log.Fatalf("Ping mongodb failed: %v", err)
		return err
	}
	MDB = &mongoDB{
		Mongo: client,
	}

	cl, err := client.Database(db).ListCollectionNames(ctx, bson.M{"name": "machine_tm"})
	if err != nil {
		log.Log.Fatalf("List mongodb collection names failed: %v", err)
		return err
	}
	if len(cl) == 0 {
		// Create collection with time series for machine info
		tsOpts := options.TimeSeries()
		tsOpts.SetTimeField("timestamp")
		tsOpts.SetMetaField("machine")
		tsOpts.SetGranularity("minutes")
		// tsOpts.SetBucketMaxSpan(30)
		// tsOpts.SetBucketRounding(5)
		ccOpts := options.CreateCollection()
		ccOpts.SetTimeSeriesOptions(tsOpts)
		ccOpts.SetExpireAfterSeconds(eas)
		if err := client.Database(db).CreateCollection(ctx, "machine_tm", ccOpts); err != nil {
			log.Log.Fatalf("Create time series collection failed: %v", err)
			return err
		}
		log.Log.Info("Create collection with time series success")
	}

	MDB.machineConnCollection = client.Database(db).Collection("machine_connection")
	MDB.machineInfoCollection = client.Database(db).Collection("machine_info")
	MDB.machineTMCollection = client.Database(db).Collection("machine_tm")

	// [FIX 2026-07-28 B] 启动清理【老版本遗留的无 conn_id 记录】(conn_id 不存在或为空)：本版所有写入都带
	//   非空 conn_id(client UUID)，无 conn_id 的记录只可能是升级前老版本留下的。清掉它们，避免孤儿清理对
	//   这类记录退回"按 MachineKey 删"路径引入重连 TOCTOU 误删活记录。
	//   ⚠️ 只删无 conn_id 的记录、【不动带 conn_id 的记录】——所以即使滚动重启期新老进程短暂并存，
	//      也不会误删老进程正在服务的活记录(它们都带 conn_id)。带 conn_id 的陈旧记录交给孤儿清理(conn_id
	//      精确删、~2min 内)+ 机器重连覆盖处理。
	if dres, derr := MDB.machineConnCollection.DeleteMany(ctx, bson.M{"$or": []bson.M{
		{"conn_id": bson.M{"$exists": false}},
		{"conn_id": ""},
	}}); derr != nil {
		log.Log.Warnf("startup clear legacy (no-conn_id) machine_connection records failed (non-fatal): %v", derr)
	} else if dres.DeletedCount > 0 {
		log.Log.Infof("startup cleared %d legacy (no-conn_id) machine_connection records", dres.DeletedCount)
	}

	// [FIX 2026-07-28 B] machine_connection 加唯一索引 {machine_id,project,container_id}：杜绝并发上线
	//   各自 upsert(都没命中已存记录时)插出重复记录(重复记录会让掉线机器逃过下线)。best-effort：若历史遗留
	//   重复导致建索引失败，只告警不致命(孤儿清理 + 所有权感知清理仍保安全)，清掉重复后重启即生效。
	{
		idxModel := mongo.IndexModel{
			Keys:    bson.D{{Key: "machine_id", Value: 1}, {Key: "project", Value: 1}, {Key: "container_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uniq_machine_key"),
		}
		if _, ierr := MDB.machineConnCollection.Indexes().CreateOne(ctx, idxModel); ierr != nil {
			log.Log.Warnf("create unique index on machine_connection failed (likely pre-existing duplicates; non-fatal, clean dups then restart to activate): %v", ierr)
		} else {
			log.Log.Info("ensured unique index uniq_machine_key on machine_connection")
		}
	}
	return nil
}

func (db *mongoDB) Disconnect(ctx context.Context) error {
	return db.Mongo.Disconnect(ctx)
}

func (db *mongoDB) IsMachineConnected(ctx context.Context, machine types.MachineKey) bool {
	result := types.MDBMachineOnline{}
	if err := db.machineConnCollection.FindOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
	).Decode(&result); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false
		}
		return false
	}
	return true
}

// AllConnectionRecords 返回 machine_connection 里所有记录(含 MachineKey + ConnId)。供孤儿清理:
// 找出"有记录但已无活连接所有者"的机器，按【记录自己的 conn_id 精确删】——这样即使清理与"新连接重连
// 覆盖记录"竞态，新连接写的是不同 conn_id、不会被误删。[FIX 2026-07-28 B]
func (db *mongoDB) AllConnectionRecords(ctx context.Context) ([]types.MDBMachineOnline, error) {
	cur, err := db.machineConnCollection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []types.MDBMachineOnline
	for cur.Next(ctx) {
		var d types.MDBMachineOnline
		if derr := cur.Decode(&d); derr != nil {
			continue
		}
		out = append(out, d)
	}
	return out, cur.Err()
}

func (db *mongoDB) MachineConnected(ctx context.Context, machine types.MachineKey, connId string) error {
	// [FIX 2026-07-28] 改 InsertOne → ReplaceOne(upsert)：机器重启重连时，若旧记录还残留（旧连接
	//   尚未被读超时清理），原子地「用本连接的记录覆盖它」而不是插入重复。配合 handleOnlineRequest
	//   不再拒绝 repeated connection、改为替换，彻底解决"重启后被锁死上不了 DDN"。写入本连接 ConnId，
	//   让后续 MachineDisconnected 只删自己那条。filter 按 MachineKey 唯一定位。
	filter := bson.M{
		"machine_id":   machine.MachineId,
		"project":      machine.Project,
		"container_id": machine.ContainerId,
	}
	res, err := db.machineConnCollection.ReplaceOne(
		ctx,
		filter,
		types.MDBMachineOnline{
			MachineKey: machine,
			AddTime:    time.Now(),
			ConnId:     connId,
		},
		options.Replace().SetUpsert(true),
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("upsert online failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine, "connId": connId, "upsertedId": res.UpsertedID, "modified": res.ModifiedCount}).Info("upserted online record")

	update, err := db.machineInfoCollection.UpdateOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
		bson.M{
			"$set": bson.M{
				"last_disconnect_time": time.Time{},
			},
		},
		// options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("reset last_disconnect_time failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("reset last_disconnect_time count ", update.ModifiedCount)
	return nil
}

func (db *mongoDB) MachineDisconnected(ctx context.Context, machine types.MachineKey, connId string) error {
	// [FIX 2026-07-28] 连接感知删除：只删「本连接(conn_id 匹配)」那条记录。
	//   场景：机器重启重连时，新连接会先 close 掉旧连接并 upsert 出「新 conn_id」的记录；旧连接的
	//   readPump 退出后异步调本函数——若仍按 MachineKey 无条件删，会把新连接刚写的记录误删、
	//   导致在线机器被判离线。加 conn_id 过滤后旧连接只能删掉它自己那条(或已被新记录覆盖=删 0 条)，
	//   新记录安然无恙。
	//   兼容：connId=="" 时退回按 MachineKey 删（老调用点/极老残留 doc 无 conn_id），不改变旧行为。
	filter := bson.M{
		"machine_id":   machine.MachineId,
		"project":      machine.Project,
		"container_id": machine.ContainerId,
	}
	if connId != "" {
		filter["conn_id"] = connId
	}
	result, err := db.machineConnCollection.DeleteOne(ctx, filter)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine, "connId": connId}).Error("delete online failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine, "connId": connId}).Info("delete online count ", result.DeletedCount)

	// update, err := db.machineInfoCollection.UpdateOne(
	// 	ctx,
	// 	bson.M{
	// 		"machine_id":   machine.MachineId,
	// 		"project":      machine.Project,
	// 		"container_id": machine.ContainerId,
	// 	},
	// 	bson.M{
	// 		"$set": bson.M{
	// 			"last_disconnect_time": time.Now(),
	// 		},
	// 	},
	// 	// options.Update().SetUpsert(true),
	// )
	// if err != nil {
	// 	log.Log.WithFields(logrus.Fields{"machine": machine}).Error("update last_disconnect_time failed: ", err)
	// 	return err
	// }
	// log.Log.WithFields(logrus.Fields{"machine": machine}).Info("update last_disconnect_time count ", update.ModifiedCount)
	return nil
}

func (db *mongoDB) GetMachineInfo(ctx context.Context, machine types.MachineKey) (*types.MDBMachineInfo, error) {
	result := &types.MDBMachineInfo{}
	if err := db.machineInfoCollection.FindOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
	).Decode(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (db *mongoDB) RegisterMachine(ctx context.Context, machine types.MachineKey, stakingType types.StakingType) error {
	res, err := db.machineInfoCollection.InsertOne(ctx, types.MDBMachineInfo{
		MachineKey:                      machine,
		StakingType:                     uint8(stakingType),
		RegisterTime:                    time.Now(),
		MDBDeepLinkMachineInfoST:        types.MDBDeepLinkMachineInfoST{},
		MDBDeepLinkMachineInfoBandwidth: types.MDBDeepLinkMachineInfoBandwidth{},
	})
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("insert machine info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("inserted machine info id ", res.InsertedID)
	return nil
}

func (db *mongoDB) UnregisterMachine(ctx context.Context, machine types.MachineKey) error {
	result, err := db.machineInfoCollection.DeleteOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("delete machine info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("delete machine info count ", result.DeletedCount)
	return nil
}

func (db *mongoDB) SetMachineInfo(
	ctx context.Context,
	machine types.MachineKey,
	deeplink_st *types.MDBDeepLinkMachineInfoST,
	deeplink_bw *types.MDBDeepLinkMachineInfoBandwidth,
) error {
	update, err := db.machineInfoCollection.UpdateOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
		bson.M{
			"$set": bson.M{
				"deeplink_st": deeplink_st,
				"deeplink_bw": deeplink_bw,
			},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("set machine info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("set machine info count ", update.ModifiedCount)
	return nil
}

func (db *mongoDB) ReadDelayOffline(ctx context.Context, call func(types.MachineKey, time.Time, types.StakingType)) error {
	cur, err := db.machineInfoCollection.Find(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("get machine info iterater cursor failed: %v", err)
	}
	for cur.Next(ctx) {
		result := types.MDBMachineInfo{}
		if err := cur.Decode(&result); err != nil {
			return fmt.Errorf("decode machine info cursor into struct failed: %v", err)
		} else {
			// log.Log.Printf("cursor %v -> %v", result, cur.Current.Lookup("_id"))
			if !result.LastDisconnectTime.IsZero() {
				// call(result.MachineKey, time.Now(), types.StakingType(result.StakingType))
				call(result.MachineKey, time.Now(), types.StakingType(result.StakingType))
			}
		}
	}
	if err := cur.Err(); err != nil {
		return fmt.Errorf("machine info cursor error: %v", err)
	}
	cur.Close(ctx)
	return nil
}

func (db *mongoDB) WriteAllDelayOffline(ctx context.Context, tm time.Time) error {
	update, err := db.machineInfoCollection.UpdateMany(
		ctx,
		bson.M{},
		bson.M{
			"$set": bson.M{
				"last_disconnect_time": tm,
			},
		},
		// options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Log.Error("update all last_disconnect_time failed: ", err)
		return err
	}
	log.Log.Info("update all last_disconnect_time count ", update.ModifiedCount)
	return nil
}

func (db *mongoDB) OfflineMachine(ctx context.Context, machine types.MachineKey, tm time.Time) error {
	update, err := db.machineInfoCollection.UpdateOne(
		ctx,
		bson.M{
			"machine_id":   machine.MachineId,
			"project":      machine.Project,
			"container_id": machine.ContainerId,
		},
		bson.M{
			"$set": bson.M{
				"last_offline_time": tm,
			},
		},
		// options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": machine}).Error("update last_offline_time failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": machine}).Info("update last_offline_time count ", update.ModifiedCount)
	return nil
}

// GetMachinesWithoutST 返回已注册但缺少 deeplink_st 硬件信息的机器列表
func (db *mongoDB) GetMachinesWithoutST(ctx context.Context) ([]types.MDBMachineInfo, error) {
	filter := bson.M{
		"$or": []bson.M{
			{"deeplink_st.calc_point": bson.M{"$exists": false}},
			{"deeplink_st.calc_point": 0},
		},
	}
	cursor, err := db.machineInfoCollection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	var results []types.MDBMachineInfo
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (db *mongoDB) AddMachineTM(ctx context.Context, mtm types.MDBMachineTM) error {
	result, err := db.machineTMCollection.InsertOne(
		ctx,
		mtm,
	)
	if err != nil {
		log.Log.WithFields(logrus.Fields{"machine": mtm.Machine}).Error("insert machine tm info failed: ", err)
		return err
	}
	log.Log.WithFields(logrus.Fields{"machine": mtm.Machine}).Info("inserted machine tm info id ", result.InsertedID)
	return nil
}

func (db *mongoDB) DeleteExpiredMachineTM(ctx context.Context, tm time.Time) error {
	result, err := db.machineTMCollection.DeleteMany(
		ctx,
		bson.M{
			"timestamp": bson.M{"$lt": tm},
		},
	)
	if err != nil {
		log.Log.Errorf("Delete expired documents before %v manully failed: %v", tm, err)
		return err
	}
	log.Log.Infof("Delete expired documents before %v manully DeletedCount %v", tm, result.DeletedCount)
	return nil
}

func (db *mongoDB) GetAllLatestMachineTM(ctx context.Context) []types.MDBMachineTM {
	di := make([]types.MDBMachineTM, 0)
	pipeline := mongo.Pipeline{
		// {{"$match", bson.D{{"timestamp", bson.D{{"$gt", specificTimestamp}}}}}},
		{{"$sort", bson.D{{"machine.machine_id", 1}, {"timestamp", -1}}}},
		{{"$group", bson.D{
			{"_id", "$machine.machine_id"},
			{"latestRecord", bson.D{{"$first", "$$ROOT"}}},
		}}},
		{{"$replaceRoot", bson.D{{"newRoot", "$latestRecord"}}}},
	}
	cursor, err := db.machineTMCollection.Aggregate(ctx, pipeline)
	if err != nil {
		log.Log.Errorf("Aggregate documents of all latest machine info failed: %v", err)
		return di
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		result := types.MDBMachineTM{}
		if err := cursor.Decode(&result); err != nil {
			log.Log.Errorf("Decode aggregate cursor into struct failed: %v", err)
		} else {
			di = append(di, result)
		}
	}
	if err := cursor.Err(); err != nil {
		log.Log.Errorf("Traversal aggregate cursor failed: %v", err)
	}
	return di
}
