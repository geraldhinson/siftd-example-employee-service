package models

import (
	"github.com/geraldhinson/siftd-base/pkg/resourceStore"
)

// used by caller when creating a new resource
type Employee struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// Define struct that embeds ResourceBase
type EmployeeResource struct {
	resourceStore.ResourceBase
	Employee Employee `json:"employee"`
}

// defining EmployeeResource as above yields the following when marshaled to JSON:
//{
//    "id": "8c7736ef-af57-43d3-9f7e-1c870dd962b2",
//    "ownerId": "9c7736ef-af57-43d3-9f7e-1c870dd962b2",
//    "version": 1,
//    "createdAt": "2025-03-25T22:06:35.438076Z",
//    "updatedAt": "2025-03-25T22:06:35.438076Z",
//    "deleted": false,
//    "employee": {
//        "name": "Alice",
//        "age": 30
//    }
//}
