package service

import (
	"encoding/json"
	"os"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/util/common"

	"gorm.io/gorm"
)

type EndpointService struct {
	WarpService
}

func (o *EndpointService) GetAll() (*[]map[string]interface{}, error) {
	db := database.GetDB()
	endpoints := []*model.Endpoint{}
	err := db.Model(model.Endpoint{}).Scan(&endpoints).Error
	if err != nil {
		return nil, err
	}
	var data []map[string]interface{}
	for _, endpoint := range endpoints {
		epData := map[string]interface{}{
			"id":   endpoint.Id,
			"type": endpoint.Type,
			"tag":  endpoint.Tag,
			"ext":  endpoint.Ext,
		}
		if endpoint.Options != nil {
			var restFields map[string]json.RawMessage
			if err := json.Unmarshal(endpoint.Options, &restFields); err != nil {
				return nil, err
			}
			for k, v := range restFields {
				epData[k] = v
			}
		}
		data = append(data, epData)
	}
	return &data, nil
}

func (o *EndpointService) GetAllConfig(db *gorm.DB) ([]json.RawMessage, error) {
	var endpointsJson []json.RawMessage
	var endpoints []*model.Endpoint
	err := db.Model(model.Endpoint{}).Scan(&endpoints).Error
	if err != nil {
		return nil, err
	}
	for _, endpoint := range endpoints {
		if endpoint.Type == "wireguard" || endpoint.Type == "warp" {
			if !repairWireGuardEndpoint(db, endpoint) {
				logger.Warningf("Skipping invalid endpoint %s: failed validation or repair", endpoint.Tag)
				continue
			}
		}

		endpointJson, err := endpoint.MarshalJSON()
		if err != nil {
			logger.Warningf("Failed to marshal endpoint %s: %v", endpoint.Tag, err)
			continue
		}
		endpointsJson = append(endpointsJson, endpointJson)
	}
	return endpointsJson, nil
}

func repairWireGuardEndpoint(db *gorm.DB, ep *model.Endpoint) bool {
	if ep.Options == nil {
		return false
	}
	var optMap map[string]interface{}
	if err := json.Unmarshal(ep.Options, &optMap); err != nil {
		return false
	}

	needsUpdate := false

	// 1. Check & ensure private_key
	privKey, _ := optMap["private_key"].(string)
	if privKey == "" {
		newPriv, _, err := GenerateNewWireGuardKeyPair()
		if err == nil {
			optMap["private_key"] = newPriv
			needsUpdate = true
			logger.Infof("Repaired endpoint %s: generated missing private key", ep.Tag)
		} else {
			return false
		}
	}

	// 2. Check & fix address / local_address
	if _, hasAddr := optMap["address"]; !hasAddr {
		if localAddr, hasLocal := optMap["local_address"]; hasLocal {
			optMap["address"] = localAddr
			delete(optMap, "local_address")
			needsUpdate = true
		} else {
			optMap["address"] = []string{"10.2.0.2/32", "2a07:b944::2:2/128"}
			needsUpdate = true
		}
	}

	// 3. Check & fix peers
	peersRaw, hasPeers := optMap["peers"].([]interface{})
	if !hasPeers || len(peersRaw) == 0 {
		server, _ := optMap["server"].(string)
		pubKey, _ := optMap["peer_public_key"].(string)
		serverPort := 51820
		if p, ok := optMap["server_port"].(float64); ok && p > 0 {
			serverPort = int(p)
		}
		if server != "" && pubKey != "" {
			optMap["peers"] = []map[string]interface{}{
				{
					"address":                       server,
					"port":                          serverPort,
					"public_key":                    pubKey,
					"allowed_ips":                   []string{"0.0.0.0/0", "::/0"},
					"persistent_keepalive_interval": 25,
				},
			}
			delete(optMap, "server")
			delete(optMap, "server_port")
			delete(optMap, "peer_public_key")
			needsUpdate = true
			logger.Infof("Repaired endpoint %s: converted legacy peer options to peers slice", ep.Tag)
		}
	}

	// Clean legacy fields
	delete(optMap, "local_address")
	delete(optMap, "server")
	delete(optMap, "server_port")
	delete(optMap, "peer_public_key")

	if needsUpdate {
		newOptions, err := json.MarshalIndent(optMap, "", "  ")
		if err == nil {
			ep.Options = newOptions
			if db != nil {
				_ = db.Model(ep).Update("options", ep.Options).Error
			}
		}
	}

	// Final validation: must have private_key and peers
	if optMap["private_key"] == "" {
		return false
	}
	if peers, ok := optMap["peers"].([]interface{}); ok && len(peers) > 0 {
		return true
	}
	if peersMap, ok := optMap["peers"].([]map[string]interface{}); ok && len(peersMap) > 0 {
		return true
	}
	return false
}

func (s *EndpointService) Save(tx *gorm.DB, act string, data json.RawMessage) error {
	var err error

	switch act {
	case "new", "edit":
		var endpoint model.Endpoint
		err = endpoint.UnmarshalJSON(data)
		if err != nil {
			return err
		}

		if endpoint.Type == "warp" {
			if act == "new" {
				err = s.WarpService.RegisterWarp(&endpoint)
				if err != nil {
					return err
				}
			} else {
				var old_license string
				err = tx.Model(model.Endpoint{}).Select("json_extract(ext, '$.license_key')").Where("id = ?", endpoint.Id).Find(&old_license).Error
				if err != nil {
					return err
				}
				err = s.WarpService.SetWarpLicense(old_license, &endpoint)
				if err != nil {
					return err
				}
			}
		}

		if corePtr.IsRunning() {
			configData, err := endpoint.MarshalJSON()
			if err != nil {
				return err
			}
			if act == "edit" {
				var oldTag string
				err = tx.Model(model.Endpoint{}).Select("tag").Where("id = ?", endpoint.Id).Find(&oldTag).Error
				if err != nil {
					return err
				}
				err = corePtr.RemoveEndpoint(oldTag)
				if err != nil && err != os.ErrInvalid {
					return err
				}
			}
			err = corePtr.AddEndpoint(configData)
			if err != nil {
				return err
			}
		}

		err = tx.Save(&endpoint).Error
		if err != nil {
			return err
		}
	case "del":
		var tag string
		err = json.Unmarshal(data, &tag)
		if err != nil {
			return err
		}
		if corePtr.IsRunning() {
			err = corePtr.RemoveEndpoint(tag)
			if err != nil && err != os.ErrInvalid {
				return err
			}
		}
		err = tx.Where("tag = ?", tag).Delete(model.Endpoint{}).Error
		if err != nil {
			return err
		}
	default:
		return common.NewErrorf("unknown action: %s", act)
	}
	return nil
}
