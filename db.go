package sqlcom

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// DB is a db
type DB struct {
	*sql.DB
}

// QueryTo query and parse data to the dest
func (db *DB) QueryTo(dest interface{}, sqlStr string, args ...interface{}) (err error) {
	destV := reflect.ValueOf(dest)
	if destV.Kind() != reflect.Ptr || destV.Elem().Kind() != reflect.Slice {
		return errors.New("dest argument must be a slice address")
	}
	sliceV := destV.Elem()
	elemT := sliceV.Type().Elem()

	if elemT.Kind() != reflect.Map && (elemT.Kind() != reflect.Ptr || elemT.Elem().Kind() != reflect.Struct) {
		return errors.New("slice element must be map or struct address")
	}

	row, err := db.Query(sqlStr, args...)
	if err != nil {
		return err
	}
	defer row.Close()

	cols, err := row.Columns()
	if err != nil {
		return err
	}

	for row.Next() {
		results := make([]interface{}, len(cols))
		for i := 0; i < len(cols); i++ {
			var v interface{}
			results[i] = &v
		}
		err = row.Scan(results...)
		if err != nil {
			return err
		}

		elemV, err := newElemFillResults(elemT, cols, results)
		if err != nil {
			return err
		}
		sliceV = reflect.Append(sliceV, elemV)
	}

	reflect.ValueOf(dest).Elem().Set(sliceV)
	return nil
}

func newElemFillResults(elemT reflect.Type, cols []string, results []interface{}) (reflect.Value, error) {
	//Map
	if elemT.Kind() == reflect.Map {
		elemV := reflect.MakeMap(elemT)
		for i, field := range cols {
			resultV := reflect.ValueOf(reflect.Indirect(reflect.ValueOf(results[i])).Interface())
			switch resultV.Kind() {
			case reflect.Slice:
				elemV.SetMapIndex(reflect.ValueOf(field), reflect.ValueOf(string(resultV.Interface().([]byte))))
			default:
				elemV.SetMapIndex(reflect.ValueOf(field), resultV)
			}

		}
		return elemV, nil
	}

	//Struct address
	elemT = elemT.Elem()
	elemV := reflect.New(elemT).Elem()
	cacheIdx := map[string]int{}
	cacheTag := map[string]string{}
	numField := elemT.NumField()
	for i := 0; i < numField; i++ {
		field := elemT.Field(i)
		name := field.Name
		if dbTag := field.Tag.Get("db"); dbTag != "" {
			vals := strings.Split(dbTag, ",")
			if len(vals) > 0 {
				name = vals[0]
			}
			if len(vals) > 1 {
				cacheTag[name] = vals[1]
			}
		}
		cacheIdx[name] = i
	}

	for i, name := range cols {
		if idx, ok := cacheIdx[name]; ok {
			field := elemV.Field(idx)
			resultV := reflect.ValueOf(reflect.Indirect(reflect.ValueOf(results[i])).Interface())
			if tag, ok := cacheTag[name]; ok {
				switch tag {
				case "json":
					if field.Kind() == reflect.Map {
						fieldV := reflect.MakeMap(field.Type())
						fitf := fieldV.Interface()
						err := json.Unmarshal(resultV.Interface().([]byte), &fitf)
						if err != nil {
							return elemV.Addr(), err
						}
						field.Set(reflect.ValueOf(fitf))
					} else if field.Kind() == reflect.Slice {
						fieldV := reflect.MakeSlice(field.Type(), 0, resultV.Len())
						fitf := fieldV.Interface()
						err := json.Unmarshal(resultV.Interface().([]byte), &fitf)
						if err != nil {
							return elemV.Addr(), err
						}
						fitfV := reflect.ValueOf(fitf)
						for i := 0; i < fitfV.Len(); i++ {
							fieldV = reflect.Append(fieldV, fitfV.Index(i).Elem())
						}
						field.Set(fieldV)
					} else {
						return elemV.Addr(), fmt.Errorf("in json tag, field %v of struct type %v is illegal", elemT.Field(idx), field.Kind())
					}
				case "time":
					ti, err := time.ParseInLocation("2006-01-02 15:04:05", string(resultV.Interface().([]byte)), time.Local)
					if err != nil {
						return elemV.Addr(), err
					}
					if field.Kind() == reflect.Int64 {
						field.Set(reflect.ValueOf(ti.UnixNano() / 1e6))
					} else {
						return elemV.Addr(), fmt.Errorf("in time tag, field %v of struct type %v is illegal", elemT.Field(idx), field.Kind())
					}
				}
			} else if field.Kind() == resultV.Kind() {
				field.Set(resultV)
			} else {
				switch field.Kind() {
				case reflect.String:
					field.Set(reflect.ValueOf(string(resultV.Interface().([]byte))))
				case reflect.Int:
					field.Set(reflect.ValueOf(int(resultV.Int())))
				case reflect.Uint:
					field.Set(reflect.ValueOf(uint(resultV.Uint())))
				default:
					return elemV.Addr(), fmt.Errorf("field '%v' type %v not match result type %v", name, field.Kind(), resultV.Kind())
				}
			}
		}
	}

	return elemV.Addr(), nil
}