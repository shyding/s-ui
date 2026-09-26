package service

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/util"
	"github.com/alireza0/s-ui/util/common"

	"gorm.io/gorm"
)

type InboundService struct {
	ClientService
}

func (s *InboundService) Get(ids string) (*[]map[string]interface{}, error) {
	if ids == "" {
		return s.GetAll()
	}
	return s.getById(ids)
}

func (s *InboundService) getById(ids string) (*[]map[string]interface{}, error) {
	var inbound []model.Inbound
	var result []map[string]interface{}
	db := database.GetDB()
	err := db.Model(model.Inbound{}).Where("id in ?", strings.Split(ids, ",")).Scan(&inbound).Error
	if err != nil {
		return nil, err
	}
	for _, inb := range inbound {
		inbData, err := inb.MarshalFull()
		if err != nil {
			return nil, err
		}
		result = append(result, *inbData)
	}
	return &result, nil
}

func (s *InboundService) GetAll() (*[]map[string]interface{}, error) {
	db := database.GetDB()
	inbounds := []model.Inbound{}
	err := db.Model(model.Inbound{}).Scan(&inbounds).Error
	if err != nil {
		return nil, err
	}
	var data []map[string]interface{}
	for _, inbound := range inbounds {
		var shadowtls_version uint
		ss_managed := false
		inbData := map[string]interface{}{
			"id":     inbound.Id,
			"type":   inbound.Type,
			"tag":    inbound.Tag,
			"tls_id": inbound.TlsId,
		}
		if inbound.Options != nil {
			var restFields map[string]json.RawMessage
			if err := json.Unmarshal(inbound.Options, &restFields); err != nil {
				return nil, err
			}
			inbData["listen"] = restFields["listen"]
			inbData["listen_port"] = restFields["listen_port"]
			if inbound.Type == "shadowtls" {
				json.Unmarshal(restFields["version"], &shadowtls_version)
			}
			if inbound.Type == "shadowsocks" {
				json.Unmarshal(restFields["managed"], &ss_managed)
			}
		}
		if s.hasUser(inbound.Type) &&
			!(inbound.Type == "shadowtls" && shadowtls_version < 3) &&
			!(inbound.Type == "shadowsocks" && ss_managed) {
			users := []string{}
			err = db.Raw("SELECT clients.name FROM clients, json_each(clients.inbounds) as je WHERE je.value = ?", inbound.Id).Scan(&users).Error
			if err != nil {
				return nil, err
			}
			inbData["users"] = users
		}

		data = append(data, inbData)
	}
	return &data, nil
}

func (s *InboundService) FromIds(ids []uint) ([]*model.Inbound, error) {
	db := database.GetDB()
	inbounds := []*model.Inbound{}
	err := db.Model(model.Inbound{}).Where("id in ?", ids).Scan(&inbounds).Error
	if err != nil {
		return nil, err
	}
	return inbounds, nil
}

func (s *InboundService) Save(tx *gorm.DB, act string, data json.RawMessage, initUserIds string, hostname string) error {
	var err error

	switch act {
	case "new", "edit":
		var inbound model.Inbound
		err = inbound.UnmarshalJSON(data)
		if err != nil {
			return err
		}
		if inbound.TlsId > 0 {
			err = tx.Model(model.Tls{}).Where("id = ?", inbound.TlsId).Find(&inbound.Tls).Error
			if err != nil {
				return err
			}
		}
		var oldTag string
		if act == "edit" {
			err = tx.Model(model.Inbound{}).Select("tag").Where("id = ?", inbound.Id).Find(&oldTag).Error
			if err != nil {
				return err
			}
		}

		if corePtr.IsRunning() {
			if act == "edit" {
				err = corePtr.RemoveInbound(oldTag)
				if err != nil && err != os.ErrInvalid {
					return err
				}
			}

			inboundConfig, err := inbound.MarshalJSON()
			if err != nil {
				return err
			}

			if act == "edit" {
				inboundConfig, err = s.addUsers(tx, inboundConfig, inbound.Id, inbound.Type)
			} else {
				inboundConfig, err = s.initUsers(tx, inboundConfig, initUserIds, inbound.Type)
			}
			if err != nil {
				return err
			}

			err = corePtr.AddInbound(inboundConfig)
			if err != nil {
				return err
			}
		}

		err = util.FillOutJson(&inbound, hostname)
		if err != nil {
			return err
		}

		err = tx.Save(&inbound).Error
		if err != nil {
			return err
		}
		switch act {
		case "new":
			err = s.ClientService.UpdateClientsOnInboundAdd(tx, initUserIds, inbound.Id, hostname)
		case "edit":
			err = s.ClientService.UpdateLinksByInboundChange(tx, &[]model.Inbound{inbound}, hostname, oldTag)
		}
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
			err = corePtr.RemoveInbound(tag)
			if err != nil && err != os.ErrInvalid {
				return err
			}
		}
		var id uint
		err = tx.Model(model.Inbound{}).Select("id").Where("tag = ?", tag).Scan(&id).Error
		if err != nil {
			return err
		}
		err = s.ClientService.UpdateClientsOnInboundDelete(tx, id, tag)
		if err != nil {
			return err
		}
		err = tx.Where("tag = ?", tag).Delete(model.Inbound{}).Error
		if err != nil {
			return err
		}
	default:
		return common.NewErrorf("unknown action: %s", act)
	}
	return nil
}

func (s *InboundService) UpdateOutJsons(tx *gorm.DB, inboundIds []uint, hostname string) error {
	var inbounds []model.Inbound
	err := tx.Model(model.Inbound{}).Preload("Tls").Where("id in ?", inboundIds).Find(&inbounds).Error
	if err != nil {
		return err
	}
	for _, inbound := range inbounds {
		err = util.FillOutJson(&inbound, hostname)
		if err != nil {
			return err
		}
		err = tx.Model(model.Inbound{}).Where("tag = ?", inbound.Tag).Update("out_json", inbound.OutJson).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *InboundService) GetAllConfig(db *gorm.DB) ([]json.RawMessage, error) {
	var inboundsJson []json.RawMessage
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("Tls").Find(&inbounds).Error
	if err != nil {
		return nil, err
	}
	for _, inbound := range inbounds {
		// Naive, Hysteria, TUIC, Hysteria2 strictly require a valid TLS certificate for QUIC server
		// If Tls is missing, disabled, or empty, sing-box crashes on startup with: "TLS is required for QUIC server"
		if inbound.Type == "naive" || inbound.Type == "hysteria" || inbound.Type == "tuic" || inbound.Type == "hysteria2" {
			hasValidTLS := false
			if inbound.Tls != nil && len(inbound.Tls.Server) > 4 && string(inbound.Tls.Server) != "null" {
				var tlsMap map[string]interface{}
				if err := json.Unmarshal(inbound.Tls.Server, &tlsMap); err == nil {
					enabled, ok := tlsMap["enabled"].(bool)
					if !ok || enabled {
						certPath, _ := tlsMap["certificate_path"].(string)
						certs, _ := tlsMap["certificate"].([]interface{})
						acme, _ := tlsMap["acme"].(map[string]interface{})
						if certPath != "" || len(certs) > 0 || len(acme) > 0 {
							hasValidTLS = true
						}
					}
				}
			}
			if !hasValidTLS {
				logger.Warningf("Skipping inbound %s (%s): missing required TLS configuration for QUIC server", inbound.Tag, inbound.Type)
				continue
			}
		}

		inboundJson, err := inbound.MarshalJSON()
		if err != nil {
			return nil, err
		}
		inboundJson, err = s.addUsers(db, inboundJson, inbound.Id, inbound.Type)
		if err != nil {
			return nil, err
		}
		inboundsJson = append(inboundsJson, inboundJson)
	}
	return inboundsJson, nil
}

func (s *InboundService) hasUser(inboundType string) bool {
	switch inboundType {
	case "mixed", "socks", "http", "shadowsocks", "vmess", "trojan", "naive", "hysteria", "shadowtls", "tuic", "hysteria2", "vless", "anytls":
		return true
	}
	return false
}

func (s *InboundService) fetchUsers(db *gorm.DB, inboundType string, condition string, inbound map[string]interface{}) ([]json.RawMessage, error) {
	if inboundType == "shadowtls" {
		version, _ := inbound["version"].(float64)
		if int(version) < 3 {
			return nil, nil
		}
	}
	if inboundType == "shadowsocks" {
		method, _ := inbound["method"].(string)
		if method == "2022-blake3-aes-128-gcm" {
			inboundType = "shadowsocks16"
		}
	}

	type clientUserRow struct {
		Name   string `gorm:"column:name"`
		Config string `gorm:"column:config"`
	}
	var rows []clientUserRow

	err := db.Raw(
		fmt.Sprintf(`SELECT name, json_extract(clients.config, "$.%s") AS config
		FROM clients WHERE enable = true AND %s`,
			inboundType, condition)).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	var usersJson []json.RawMessage
	for _, row := range rows {
		if strings.TrimSpace(row.Config) == "" || row.Config == "null" {
			continue
		}
		var userMap map[string]interface{}
		if err := json.Unmarshal([]byte(row.Config), &userMap); err != nil {
			continue
		}
		// Populate client name so regional expansion and auth_user rules match
		userMap["name"] = row.Name
		if inboundType == "vless" && inbound["tls"] == nil {
			if flow, ok := userMap["flow"].(string); ok && strings.Contains(flow, "xtls-rprx-vision") {
				userMap["flow"] = ""
			}
		}
		if raw, err := json.Marshal(userMap); err == nil {
			usersJson = append(usersJson, json.RawMessage(raw))
		}
	}
	return usersJson, nil
}

func (s *InboundService) addUsers(db *gorm.DB, inboundJson []byte, inboundId uint, inboundType string) ([]byte, error) {
	if !s.hasUser(inboundType) {
		return inboundJson, nil
	}

	var inbound map[string]interface{}
	err := json.Unmarshal(inboundJson, &inbound)
	if err != nil {
		return nil, err
	}

	condition := fmt.Sprintf("%d IN (SELECT json_each.value FROM json_each(clients.inbounds))", inboundId)
	users, err := s.fetchUsers(db, inboundType, condition, inbound)
	if err != nil {
		return nil, err
	}
	activeRegions := GetActiveEgressRegions(db)
	inbound["users"] = ExpandUsersForMultiplexing(users, inboundType, activeRegions)

	return json.Marshal(inbound)
}

func (s *InboundService) initUsers(db *gorm.DB, inboundJson []byte, clientIds string, inboundType string) ([]byte, error) {
	ClientIds := strings.Split(clientIds, ",")
	if len(ClientIds) == 0 {
		return inboundJson, nil
	}

	if !s.hasUser(inboundType) {
		return inboundJson, nil
	}

	var inbound map[string]interface{}
	err := json.Unmarshal(inboundJson, &inbound)
	if err != nil {
		return nil, err
	}

	condition := fmt.Sprintf("id IN (%s)", strings.Join(ClientIds, ","))
	users, err := s.fetchUsers(db, inboundType, condition, inbound)
	if err != nil {
		return nil, err
	}
	activeRegions := GetActiveEgressRegions(db)
	inbound["users"] = ExpandUsersForMultiplexing(users, inboundType, activeRegions)

	return json.Marshal(inbound)
}

func (s *InboundService) RestartInbounds(tx *gorm.DB, ids []uint) error {
	if !corePtr.IsRunning() {
		return nil
	}
	var inbounds []*model.Inbound
	err := tx.Model(model.Inbound{}).Preload("Tls").Where("id in ?", ids).Find(&inbounds).Error
	if err != nil {
		return err
	}
	for _, inbound := range inbounds {
		err = corePtr.RemoveInbound(inbound.Tag)
		if err != nil && err != os.ErrInvalid {
			return err
		}
		// Close all existing connections
		corePtr.GetInstance().ConnTracker().CloseConnByInbound(inbound.Tag)

		inboundConfig, err := inbound.MarshalJSON()
		if err != nil {
			return err
		}
		inboundConfig, err = s.addUsers(tx, inboundConfig, inbound.Id, inbound.Type)
		if err != nil {
			return err
		}
		err = corePtr.AddInbound(inboundConfig)
		if err != nil {
			return err
		}
	}
	return nil
}
