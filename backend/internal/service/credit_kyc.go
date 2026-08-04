package service

import (
	"context"
	"fmt"
	"strings"

	"sub2api-account-monitor/internal/repository"
)

// KYC 主体类型。
const (
	SubjectTypeIndividual = "individual" // 个人
	SubjectTypeCompany    = "company"    // 公司
)

// KYC 审核状态。
//
// draft → pending 由客户/管理员提交触发；pending → approved | rejected 由审核触发。
// rejected 后允许重新编辑再提交，故 rejected → pending 亦合法。
const (
	KYCStatusDraft    = "draft"
	KYCStatusPending  = "pending"
	KYCStatusApproved = "approved"
	KYCStatusRejected = "rejected"
)

// validateSubjectType 校验主体类型。
func validateSubjectType(t string) error {
	if t != SubjectTypeIndividual && t != SubjectTypeCompany {
		return fmt.Errorf("%w: subject_type 须为 individual 或 company", ErrInvalidInput)
	}
	return nil
}

// kycField 一个待校验字段：label 用于拼错误信息，value 是待检查的值。
type kycField struct {
	label string
	value string
}

// requiredKYCFields 按主体类型返回必填字段（提交送审时校验，存草稿不校验）。
//
// 两种主体的必填集合刻意分开列举而非用「非空即可」的通用规则：
// 公司填了法人姓名不代表个人身份已核实，反之亦然，混填会让审核依据失真。
func requiredKYCFields(p *repository.KYCParams) []kycField {
	common := []kycField{
		{"country_region", p.CountryRegion},
		{"contact_name", p.ContactName},
		{"contact_phone", p.ContactPhone},
		{"contact_email", p.ContactEmail},
	}
	if p.SubjectType == SubjectTypeCompany {
		return append(common,
			kycField{"company_name", p.CompanyName},
			kycField{"reg_number", p.RegNumber},
			kycField{"legal_rep", p.LegalRep},
			kycField{"reg_address", p.RegAddress},
		)
	}
	return append(common,
		kycField{"legal_name", p.LegalName},
		kycField{"id_type", p.IDType},
		kycField{"id_number", p.IDNumber},
	)
}

// normalizeKYC 去除各字段首尾空白（表单粘贴常带空格，会让必填校验形同虚设）。
func normalizeKYC(p *repository.KYCParams) {
	for _, ptr := range []*string{
		&p.SubjectType, &p.CountryRegion, &p.IDType,
		&p.LegalName, &p.IDNumber, &p.BirthDate, &p.Address,
		&p.CompanyName, &p.RegNumber, &p.LegalRep, &p.RegAddress, &p.TaxNumber,
		&p.ContactName, &p.ContactPhone, &p.ContactEmail,
		&p.BankName, &p.BankAccount, &p.BankHolder,
	} {
		*ptr = strings.TrimSpace(*ptr)
	}
}

// GetKYC 读取客户 KYC 资料。不存在时返回 repository.ErrNotFound。
//
// 返回值含全部解密后的 PII，仅供管理端与「本人」使用；
// 调用方负责裁剪 DTO，日志里绝不可打印本结构体。
func (s *CreditService) GetKYC(ctx context.Context, customerID int64) (*repository.CustomerKYC, error) {
	return s.repo.GetKYC(ctx, customerID)
}

// SaveKYC 保存 KYC 资料。submit=true 表示同时提交送审（置 pending）。
//
// 送审时才做必填校验：存草稿允许字段残缺，否则客户填一半就没法保存。
func (s *CreditService) SaveKYC(ctx context.Context, customerID int64, p repository.KYCParams, submit bool) (*repository.CustomerKYC, error) {
	normalizeKYC(&p)
	if p.SubjectType == "" {
		p.SubjectType = SubjectTypeIndividual
	}
	if err := validateSubjectType(p.SubjectType); err != nil {
		return nil, err
	}
	if submit {
		for _, f := range requiredKYCFields(&p) {
			if f.value == "" {
				return nil, fmt.Errorf("%w: 提交审核需填写 %s", ErrInvalidInput, f.label)
			}
		}
	}
	return s.repo.UpsertKYC(ctx, customerID, p, submit)
}

// ReviewKYC 审核：通过或驳回。
//
// 驳回必须写明理由 —— 客户看不到理由就无从修正，只会反复提交同样的内容。
func (s *CreditService) ReviewKYC(ctx context.Context, customerID int64, status, reviewer, note string) (*repository.CustomerKYC, error) {
	if status != KYCStatusApproved && status != KYCStatusRejected {
		return nil, fmt.Errorf("%w: 审核结果须为 approved 或 rejected", ErrInvalidInput)
	}
	note = strings.TrimSpace(note)
	if status == KYCStatusRejected && note == "" {
		return nil, fmt.Errorf("%w: 驳回须填写原因", ErrInvalidInput)
	}
	return s.repo.ReviewKYC(ctx, customerID, status, reviewer, note)
}
